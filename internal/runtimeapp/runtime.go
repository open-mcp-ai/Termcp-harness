package runtimeapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/controlapi"
	"github.com/open-mcp-ai/termcp-harness/internal/coreapproval"
	"github.com/open-mcp-ai/termcp-harness/internal/corebundle"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/coreprocess"
	"github.com/open-mcp-ai/termcp-harness/internal/daemon"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpdiscovery"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/recovery"
	runtimepkg "github.com/open-mcp-ai/termcp-harness/internal/runtime"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

type App struct {
	endpoint string
	cancel   context.CancelFunc
	done     chan struct{}

	closeOnce sync.Once
	errMu     sync.RWMutex
	err       error
	coreMu    sync.RWMutex
	core      CoreAccess
}

type CoreAccess struct {
	Endpoint  string
	AuthToken string
	Managed   bool
}

func (a *App) Endpoint() string {
	if a == nil {
		return ""
	}
	return a.endpoint
}

func (a *App) CoreAccess() CoreAccess {
	if a == nil {
		return CoreAccess{}
	}
	a.coreMu.RLock()
	defer a.coreMu.RUnlock()
	return a.core
}

func (a *App) setCoreAccess(core CoreAccess) {
	a.coreMu.Lock()
	a.core = core
	a.coreMu.Unlock()
}

func (a *App) Done() <-chan struct{} {
	if a == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return a.done
}

func (a *App) Err() error {
	if a == nil {
		return nil
	}
	a.errMu.RLock()
	defer a.errMu.RUnlock()
	return a.err
}

func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.closeOnce.Do(a.cancel)
	select {
	case <-a.done:
		return a.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func Run(ctx context.Context, config Config) error {
	app, err := Start(ctx, config)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-app.Done():
		return app.Err()
	}
	closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return app.Close(closeContext)
}

func Start(ctx context.Context, config Config) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	serveContext, cancel := context.WithCancel(ctx)
	app := &App{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	ready := make(chan string, 1)
	go func() {
		runErr := runServer(
			serveContext,
			config.defaults(),
			func(endpoint string) {
				select {
				case ready <- endpoint:
				default:
				}
			},
			app.setCoreAccess,
		)
		app.errMu.Lock()
		app.err = runErr
		app.errMu.Unlock()
		close(app.done)
	}()

	select {
	case endpoint := <-ready:
		app.endpoint = endpoint
		return app, nil
	case <-app.done:
		cancel()
		if err := app.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("Harness Runtime stopped before reporting readiness")
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

func runServer(
	ctx context.Context,
	config Config,
	ready func(string),
	coreReady func(CoreAccess),
) error {
	if config.Cleanup != nil {
		defer config.Cleanup()
	}
	logger := config.Logger
	if logger == nil {
		writer := config.LogWriter
		if writer == nil {
			writer = ioDiscard{}
		}
		logger = slog.New(slog.NewJSONHandler(writer, nil))
	}
	transport := mcpcontrol.TransportKind(strings.TrimSpace(string(config.MCPTransport)))
	if strings.TrimSpace(config.ToolPolicyFile) != "" &&
		strings.TrimSpace(config.ApprovalTokenFile) == "" &&
		strings.TrimSpace(config.ApprovalToken) == "" {
		return errors.New("approval-token-file or approval token is required for protected Agent Session execution")
	}
	if (strings.TrimSpace(config.ModelEndpoint) == "") != (strings.TrimSpace(config.ModelID) == "") {
		return errors.New("model endpoint and model ID must be configured together")
	}
	if strings.TrimSpace(config.ModelEndpoint) != "" && strings.TrimSpace(config.ToolPolicyFile) == "" {
		return errors.New("model endpoint requires a tool policy file for the model Agent Loop")
	}
	if strings.TrimSpace(config.ModelTokenFile) != "" && strings.TrimSpace(config.ModelEndpoint) == "" {
		return errors.New("model token file requires a model endpoint")
	}
	if config.ModelTimeout <= 0 {
		return errors.New("model timeout must be positive")
	}
	if config.ModelMaxOutputToken < 0 || config.ModelMaxOutputToken > 32768 {
		return errors.New("model max output tokens must be between 0 and 32768")
	}
	if config.MCPReconnectMin <= 0 || config.MCPReconnectMax < config.MCPReconnectMin ||
		config.MCPHealthInterval <= 0 {
		return errors.New("MCP reconnect and health intervals must be positive with max >= min")
	}
	switch transport {
	case mcpcontrol.TransportStreamableHTTP, mcpcontrol.TransportSSE:
		if strings.TrimSpace(config.MCPStdioCommand) != "" || len(config.MCPStdioArgs) != 0 ||
			strings.TrimSpace(config.MCPStdioWorkDir) != "" {
			return errors.New("HTTP MCP transport cannot include stdio command settings")
		}
	case mcpcontrol.TransportStdio:
		if strings.TrimSpace(config.MCPEndpoint) != "" || strings.TrimSpace(config.MCPAuthTokenFile) != "" {
			return errors.New("stdio MCP transport cannot include MCP endpoint or MCP auth token file")
		}
		if strings.TrimSpace(config.MCPStdioCommand) == "" {
			return errors.New("stdio MCP transport requires a stdio command")
		}
	default:
		return fmt.Errorf("unsupported MCP transport %q", transport)
	}
	additionalMCPServers, err := LoadMCPConfigFile(config.MCPConfigFile)
	if err != nil {
		return err
	}

	recoveryConfig := recovery.Config{
		ManifestPath: strings.TrimSpace(config.RecoveryManifest),
		Stores: map[recovery.StoreName]string{
			recovery.StoreEvents:    config.EventLogFile,
			recovery.StoreSessions:  config.SessionStoreFile,
			recovery.StoreApprovals: config.ApprovalStoreFile,
		},
	}
	recoveryStoreConfigured := false
	for _, path := range recoveryConfig.Stores {
		if strings.TrimSpace(path) != "" {
			recoveryStoreConfigured = true
			break
		}
	}
	if recoveryConfig.ManifestPath != "" && !recoveryStoreConfigured {
		return errors.New("recovery manifest requires at least one persistent store file")
	}
	recoverySnapshot := recovery.Inspect(recoveryConfig)
	if recoverySnapshot.ManifestConfigured && len(recoverySnapshot.Stores) > 0 {
		allMissing := true
		anyConfigured := false
		for _, store := range recoverySnapshot.Stores {
			if !store.Configured {
				continue
			}
			anyConfigured = true
			if store.Exists || store.State == recovery.StoreUnreadable {
				allMissing = false
			}
		}
		if anyConfigured && allMissing && recoverySnapshot.State == recovery.StateUnverified {
			recoverySnapshot, err = recovery.WriteManifest(recoveryConfig)
			if err != nil {
				return fmt.Errorf("initialize recovery manifest: %w", err)
			}
		}
	}
	logger.Info(
		"recovery state inspected",
		"state", recoverySnapshot.State,
		"consistent", recoverySnapshot.Consistent,
		"manifest_present", recoverySnapshot.ManifestPresent,
	)
	if recoverySnapshot.State == recovery.StatePartial {
		logger.Warn("recovery state is partial", "warning", recoverySnapshot.Warning)
	}

	var journal *eventlog.Journal
	if strings.TrimSpace(config.EventLogFile) != "" {
		eventStore, storeErr := eventlog.NewFileStore(config.EventLogFile)
		if storeErr != nil {
			return fmt.Errorf("configure Event Journal store: %w", storeErr)
		}
		journal, err = eventlog.NewWithPersistence(eventStore)
		if err != nil {
			return fmt.Errorf("load Event Journal store: %w", err)
		}
	} else {
		journal = eventlog.New()
	}
	host := mcpcontrol.NewHost(
		mcpcontrol.WithHostEventLog(journal),
		mcpcontrol.WithHostSupervision(
			config.MCPReconnectMin,
			config.MCPReconnectMax,
			config.MCPHealthInterval,
		),
	)
	workspaceDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve MCP discovery workspace: %w", err)
	}
	mcpDiscovery := mcpdiscovery.NewManager(mcpdiscovery.NewScanner(workspaceDir), host)
	coreManager := coreinstance.NewManager(journal)
	var agentSessions *agentsession.Manager
	if strings.TrimSpace(config.SessionStoreFile) != "" {
		sessionStore, storeErr := agentsession.NewFileStore(config.SessionStoreFile)
		if storeErr != nil {
			return fmt.Errorf("configure Agent Session store: %w", storeErr)
		}
		agentSessions, err = agentsession.NewManagerWithPersistence(journal, sessionStore)
	} else {
		agentSessions, err = agentsession.NewManager(journal)
	}
	if err != nil {
		return err
	}
	rt, err := runtimepkg.New(config.Version, runtimepkg.WithEventLog(journal))
	if err != nil {
		return err
	}

	serveContext, cancelServe := context.WithCancel(ctx)
	defer cancelServe()

	bundleDir, bundleConfigured, err := corebundle.Discover(config.CoreBundleDir)
	if err != nil {
		return err
	}
	resolvedCoreEndpoint := strings.TrimSpace(config.CoreEndpoint)
	resolvedMCPEndpoint := strings.TrimSpace(config.MCPEndpoint)
	mcpAuthToken := ""
	var bundledCore *coreprocess.Process
	if bundleConfigured && (resolvedCoreEndpoint != "" || resolvedMCPEndpoint != "" ||
		strings.TrimSpace(config.MCPStdioCommand) != "") {
		return errors.New("core bundle cannot be combined with an external Core or MCP endpoint")
	}
	if bundleConfigured {
		if transport != mcpcontrol.TransportStreamableHTTP {
			return errors.New("core bundle requires streamable-http MCP transport")
		}
		if strings.TrimSpace(config.MCPAuthTokenFile) != "" {
			return errors.New("MCP auth token file cannot be combined with a core bundle")
		}
		bundle, loadErr := corebundle.Load(bundleDir)
		if loadErr != nil {
			return fmt.Errorf("load bundled Core: %w", loadErr)
		}
		dataDir := strings.TrimSpace(config.CoreDataDir)
		if dataDir == "" {
			dataDir, err = coreprocess.DefaultDataDir()
			if err != nil {
				return err
			}
		}
		bundledCore, err = coreprocess.Start(serveContext, coreprocess.Config{
			BinaryPath:   bundle.BinaryPath,
			Version:      bundle.Manifest.Version,
			DataDir:      dataDir,
			LogPath:      strings.TrimSpace(config.CoreLogPath),
			NoInternal:   config.CoreNoInternal,
			StartTimeout: config.CoreStartTimeout,
		})
		if err != nil {
			return fmt.Errorf("start bundled Core: %w", err)
		}
		resolvedCoreEndpoint = bundledCore.Endpoint()
		resolvedMCPEndpoint = bundledCore.MCPEndpoint()
		mcpAuthToken = bundledCore.AuthToken()
		logger.Info(
			"bundled Core started",
			"endpoint", resolvedCoreEndpoint,
			"pid", bundledCore.PID(),
			"version", bundledCore.Version(),
			"log_path", bundledCore.LogPath(),
		)
	}
	if strings.TrimSpace(config.MCPAuthTokenFile) != "" {
		if resolvedMCPEndpoint == "" {
			return errors.New("MCP auth token file requires an MCP endpoint")
		}
		mcpAuthToken, err = tokenfile.Read(config.MCPAuthTokenFile)
		if err != nil {
			return fmt.Errorf("read MCP auth token: %w", err)
		}
	}
	coreAuthToken := ""
	if mcpAuthToken != "" && SameHTTPOrigin(resolvedCoreEndpoint, resolvedMCPEndpoint) {
		coreAuthToken = mcpAuthToken
	}
	defer func() {
		_ = host.Close()
		if bundledCore != nil {
			stopContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if stopErr := bundledCore.Stop(stopContext); stopErr != nil {
				logger.Warn("bundled Core stop failed", "error", stopErr)
			}
		}
	}()

	if resolvedCoreEndpoint == "" && resolvedMCPEndpoint != "" {
		resolvedCoreEndpoint, err = CoreOriginFromMCP(resolvedMCPEndpoint)
		if err != nil {
			return fmt.Errorf("derive Core endpoint: %w", err)
		}
	}
	if resolvedCoreEndpoint != "" {
		resolvedCoreEndpoint, err = coreinstance.ValidateEndpoint(resolvedCoreEndpoint)
		if err != nil {
			return fmt.Errorf("validate Core endpoint: %w", err)
		}
		if coreReady != nil {
			coreReady(CoreAccess{
				Endpoint:  resolvedCoreEndpoint,
				AuthToken: coreAuthToken,
				Managed:   bundledCore != nil,
			})
		}
		if bundledCore != nil {
			identityContext, cancel := context.WithTimeout(serveContext, 15*time.Second)
			snapshot, bindErr := coreManager.Bind(identityContext, coreinstance.Binding{
				Endpoint:  resolvedCoreEndpoint,
				Ownership: coreinstance.OwnershipManaged,
				Managed: &coreinstance.ManagedProcess{
					PID:       bundledCore.PID(),
					StartedAt: bundledCore.StartedAt(),
				},
				AuthToken: mcpAuthToken,
			})
			cancel()
			if bindErr != nil {
				return fmt.Errorf("bind bundled Core identity: %w", bindErr)
			}
			logger.Info(
				"bundled Core identity verified",
				"endpoint", snapshot.Endpoint,
				"pid", snapshot.PID,
				"ownership", snapshot.Ownership,
				"instance_key", snapshot.InstanceKey,
			)
			go coreManager.Run(serveContext, 10*time.Second)
			go func() {
				select {
				case <-bundledCore.Done():
					if !bundledCore.Stopping() {
						if waitErr := bundledCore.WaitErr(); waitErr != nil {
							logger.Error("bundled Core exited unexpectedly", "error", waitErr)
						} else {
							logger.Warn("bundled Core exited unexpectedly")
						}
					}
					cancelServe()
				case <-serveContext.Done():
				}
			}()
		} else {
			go func() {
				identityContext, cancel := context.WithTimeout(serveContext, 15*time.Second)
				snapshot, bindErr := coreManager.Bind(identityContext, coreinstance.Binding{
					Endpoint:  resolvedCoreEndpoint,
					Ownership: coreinstance.OwnershipExternal,
					AuthToken: coreAuthToken,
				})
				cancel()
				if bindErr != nil {
					logger.Warn("Core identity unavailable", "endpoint", resolvedCoreEndpoint, "error", bindErr)
				} else {
					logger.Info(
						"Core instance identified",
						"endpoint", snapshot.Endpoint,
						"pid", snapshot.PID,
						"ownership", snapshot.Ownership,
					)
				}
				coreManager.Run(serveContext, 10*time.Second)
			}()
		}
	}

	controlOptions := []controlapi.Option{
		controlapi.WithEventLog(journal),
		controlapi.WithRecovery(recovery.NewReader(recoverySnapshot)),
		controlapi.WithMCPRegistry(host),
		controlapi.WithMCPDiscovery(mcpDiscovery),
		controlapi.WithCore(coreManager),
		controlapi.WithAgentSessions(agentSessions),
		controlapi.WithAgentSessionTranscripts(agentSessions),
	}
	var approvalCoordinator *approval.Coordinator
	if resolvedCoreEndpoint != "" {
		approvalClient, clientErr := coreapproval.NewClient(resolvedCoreEndpoint, coreAuthToken, nil)
		if clientErr != nil {
			return fmt.Errorf("create Core approval client: %w", clientErr)
		}
		coordinatorOptions := []approval.CoordinatorOption{
			approval.WithDecisionObserver(agentSessions),
		}
		if strings.TrimSpace(config.ApprovalStoreFile) != "" {
			approvalStore, storeErr := approval.NewFileStore(config.ApprovalStoreFile)
			if storeErr != nil {
				return fmt.Errorf("configure approval history store: %w", storeErr)
			}
			coordinatorOptions = append(coordinatorOptions, approval.WithPersistence(approvalStore))
		}
		var coordinatorErr error
		approvalCoordinator, coordinatorErr = approval.NewCoordinator(
			journal,
			approvalClient,
			coordinatorOptions...,
		)
		if coordinatorErr != nil {
			return fmt.Errorf("create approval coordinator: %w", coordinatorErr)
		}
		controlOptions = append(controlOptions, controlapi.WithApprovals(approvalCoordinator))
	} else if strings.TrimSpace(config.ApprovalStoreFile) != "" {
		return errors.New("approval store file requires a configured Core endpoint or bundle")
	}
	approvalToken := strings.TrimSpace(config.ApprovalToken)
	if approvalToken == "" && strings.TrimSpace(config.ApprovalTokenFile) != "" {
		approvalToken, err = ReadApprovalToken(config.ApprovalTokenFile)
		if err != nil {
			return err
		}
	}
	if approvalToken != "" {
		authorizer, authorizerErr := controlapi.NewApprovalAuthorizer(approvalToken)
		if authorizerErr != nil {
			return fmt.Errorf("load approval authorizer: %w", authorizerErr)
		}
		controlOptions = append(controlOptions, controlapi.WithApprovalAuthorizer(authorizer))
		if approvalCoordinator != nil {
			controlOptions = append(
				controlOptions,
				controlapi.WithApprovalDecider(approvalCoordinator),
				controlapi.WithApprovalExecutionRefresher(approvalCoordinator),
				controlapi.WithApprovalReconciler(approvalCoordinator),
			)
		}
	}
	var toolInvoker agentsession.Invoker
	var toolPolicy *toolpipeline.AllowlistPolicy
	mcpConfigured := resolvedMCPEndpoint != "" || transport == mcpcontrol.TransportStdio ||
		len(additionalMCPServers) > 0
	if strings.TrimSpace(config.ToolPolicyFile) != "" {
		if !mcpConfigured {
			return errors.New("tool policy file requires a configured MCP server")
		}
		policy, policyErr := LoadToolPolicy(config.ToolPolicyFile)
		if policyErr != nil {
			return policyErr
		}
		toolPolicy = policy
		options := []toolpipeline.Option{}
		if approvalCoordinator != nil {
			options = append(options, toolpipeline.WithApprovalTracker(approvalCoordinator))
		}
		pipeline, pipelineErr := toolpipeline.New(policy, host, journal, options...)
		if pipelineErr != nil {
			return fmt.Errorf("create tool pipeline: %w", pipelineErr)
		}
		toolInvoker = pipeline
	}
	controlOptions = append(controlOptions, controlapi.WithAgentSessionRunner(agentSessions, toolInvoker))
	if strings.TrimSpace(config.ModelEndpoint) != "" {
		modelToken := ""
		if strings.TrimSpace(config.ModelTokenFile) != "" {
			modelToken, err = tokenfile.Read(config.ModelTokenFile)
			if err != nil {
				return fmt.Errorf("read model token: %w", err)
			}
		}
		adapter, adapterErr := model.NewOpenAICompatible(model.OpenAIConfig{
			Endpoint:        config.ModelEndpoint,
			Model:           config.ModelID,
			APIToken:        modelToken,
			HTTPClient:      &http.Client{Timeout: config.ModelTimeout},
			MaxOutputTokens: config.ModelMaxOutputToken,
		})
		if adapterErr != nil {
			return fmt.Errorf("configure model adapter: %w", adapterErr)
		}
		controlOptions = append(controlOptions, controlapi.WithAgentModelLoop(
			agentSessions,
			adapter,
			&PolicyModelToolProvider{Host: host, Policy: toolPolicy},
		))
	}
	server, err := daemon.NewServer(
		config.ListenAddr,
		controlapi.NewHandler(rt, controlOptions...),
		rt,
	)
	if err != nil {
		return err
	}

	if mcpConfigured {
		legacyConfigured := resolvedMCPEndpoint != "" || transport == mcpcontrol.TransportStdio
		if legacyConfigured {
			if _, err := host.Register(mcpcontrol.Spec{
				ID:        config.MCPID,
				Name:      config.MCPName,
				Endpoint:  resolvedMCPEndpoint,
				Transport: transport,
				Command:   config.MCPStdioCommand,
				Args:      append([]string(nil), config.MCPStdioArgs...),
				WorkDir:   config.MCPStdioWorkDir,
				AuthToken: mcpAuthToken,
			}); err != nil {
				return fmt.Errorf("register MCP server: %w", err)
			}
			if err := host.SetDesiredEnabled(config.MCPID, config.MCPEnable); err != nil {
				return fmt.Errorf("set MCP server desired state: %w", err)
			}
		}
		for _, configured := range additionalMCPServers {
			if _, err := host.Register(configured.Spec); err != nil {
				return fmt.Errorf("register MCP server %q: %w", configured.Spec.ID, err)
			}
			if err := host.SetDesiredEnabled(configured.Spec.ID, configured.Enabled); err != nil {
				return fmt.Errorf("set MCP server %q desired state: %w", configured.Spec.ID, err)
			}
		}
		go host.Run(serveContext)
	}
	logger.Info("starting runtime", "instance_id", rt.Snapshot().InstanceID, "listen", config.ListenAddr)
	writeRecoveryBaseline := recoverySnapshot.State != recovery.StatePartial
	runErr := server.RunWithReady(serveContext, func(endpoint string) error {
		if ready != nil {
			ready(endpoint)
		}
		return nil
	})
	cancelServe()
	_ = host.Close()
	if runErr != nil {
		return runErr
	}
	if writeRecoveryBaseline && recoverySnapshot.ManifestConfigured {
		if _, manifestErr := recovery.WriteManifest(recoveryConfig); manifestErr != nil {
			logger.Error("recovery manifest update failed", "error", manifestErr)
		} else {
			logger.Info("recovery manifest updated")
		}
	}
	logger.Info("runtime stopped", "instance_id", rt.Snapshot().InstanceID)
	return nil
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
