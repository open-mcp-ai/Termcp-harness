## 变更范围

说明这个 PR 解决的问题、涉及模块和明确不包含的内容。

## 契约与安全

- 是否改变 Core、MCP、Control API、事件、恢复或 Terminal 公开契约：
- Token、Authorization、私钥和敏感终端内容是否保持排除：
- 是否引入有副作用的重试、通用命令/文件/URL 桥或新的后台进程：

## 验证

列出实际执行的命令和结果；未执行的测试必须说明原因和风险。

```text
go test -race ./...
go vet ./...
go build ./...
cd desktop/frontend
npm run check
npm test
```

## 提交检查

- [ ] 变更范围单一，未混入无关工作区改动。
- [ ] 新增或改变的行为有对应测试，或明确记录未验证原因。
- [ ] 文档与实现状态一致，未把 Proposed / 本地验证描述成正式发布能力。
- [ ] 日志、事件、URL、前端持久化和诊断输出中没有凭据或敏感内容。
- [ ] Core 相关变更已核对 `docs/contracts/core/v0.2.8/known-issues.md`。
