param(
  [string]$ProjectRoot = (Split-Path -Parent $PSScriptRoot)
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing.Common

$buildDir = Join-Path $ProjectRoot 'build'
$windowsDir = Join-Path $buildDir 'windows'
$pngPath = Join-Path $buildDir 'appicon.png'
$icoPath = Join-Path $windowsDir 'icon.ico'

New-Item -ItemType Directory -Force -Path $buildDir, $windowsDir | Out-Null

function New-RoundedRectPath {
  param(
    [System.Drawing.RectangleF]$Rect,
    [float]$Radius
  )
  $path = [System.Drawing.Drawing2D.GraphicsPath]::new()
  $diameter = 2 * $Radius
  $path.AddArc($Rect.X, $Rect.Y, $diameter, $diameter, 180, 90)
  $path.AddArc($Rect.Right - $diameter, $Rect.Y, $diameter, $diameter, 270, 90)
  $path.AddArc($Rect.Right - $diameter, $Rect.Bottom - $diameter, $diameter, $diameter, 0, 90)
  $path.AddArc($Rect.X, $Rect.Bottom - $diameter, $diameter, $diameter, 90, 90)
  $path.CloseFigure()
  return $path
}

function New-Brush {
  param(
    [System.Drawing.Color]$Start,
    [System.Drawing.Color]$End,
    [System.Drawing.RectangleF]$Rect
  )
  return [System.Drawing.Drawing2D.LinearGradientBrush]::new(
    $Rect,
    $Start,
    $End,
    [System.Drawing.Drawing2D.LinearGradientMode]::ForwardDiagonal
  )
}

function New-Pen {
  param(
    [System.Drawing.Color]$Color,
    [float]$Width
  )
  $pen = [System.Drawing.Pen]::new($Color, $Width)
  $pen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
  $pen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
  $pen.LineJoin = [System.Drawing.Drawing2D.LineJoin]::Round
  return $pen
}

$bitmap = [System.Drawing.Bitmap]::new(
  1024,
  1024,
  [System.Drawing.Imaging.PixelFormat]::Format32bppArgb
)
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.Clear([System.Drawing.Color]::Transparent)
$graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
$graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
$graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality

$tilePath = New-RoundedRectPath -Rect ([System.Drawing.RectangleF]::new(32, 32, 960, 960)) -Radius 214
$tileBrush = New-Brush `
  -Start ([System.Drawing.Color]::FromArgb(255, 5, 19, 45)) `
  -End ([System.Drawing.Color]::FromArgb(255, 34, 9, 61)) `
  -Rect ([System.Drawing.RectangleF]::new(32, 32, 960, 960))
$graphics.FillPath($tileBrush, $tilePath)
$graphics.SetClip($tilePath)

$haloBrush = [System.Drawing.Drawing2D.PathGradientBrush]::new(
  [System.Drawing.PointF[]]@(
    [System.Drawing.PointF]::new(512, 165),
    [System.Drawing.PointF]::new(870, 512),
    [System.Drawing.PointF]::new(512, 860),
    [System.Drawing.PointF]::new(154, 512)
  )
)
$haloBrush.CenterColor = [System.Drawing.Color]::FromArgb(100, 20, 126, 255)
$haloBrush.SurroundColors = [System.Drawing.Color[]]@(
  [System.Drawing.Color]::FromArgb(0, 30, 100, 255),
  [System.Drawing.Color]::FromArgb(0, 255, 54, 187),
  [System.Drawing.Color]::FromArgb(0, 90, 53, 255),
  [System.Drawing.Color]::FromArgb(0, 45, 214, 255)
)
$graphics.FillEllipse($haloBrush, 64, 64, 896, 896)

$gridPen = New-Pen ([System.Drawing.Color]::FromArgb(28, 82, 227, 255)) 2
for ($offset = 96; $offset -lt 1024; $offset += 64) {
  $graphics.DrawLine($gridPen, $offset, 32, $offset, 992)
  $graphics.DrawLine($gridPen, 32, $offset, 992, $offset)
}

$cyan = [System.Drawing.Color]::FromArgb(255, 82, 238, 255)
$cyanSoft = [System.Drawing.Color]::FromArgb(88, 64, 225, 255)
$pink = [System.Drawing.Color]::FromArgb(255, 255, 78, 203)
$pinkSoft = [System.Drawing.Color]::FromArgb(92, 255, 66, 206)
$violet = [System.Drawing.Color]::FromArgb(255, 137, 102, 255)

$glowCyan = New-Pen $cyanSoft 34
$glowPink = New-Pen $pinkSoft 38
$cyanPen = New-Pen $cyan 14
$pinkPen = New-Pen $pink 16
$pinkGlowBrush = [System.Drawing.SolidBrush]::new([System.Drawing.Color]::FromArgb(54, 255, 44, 196))

$graphics.DrawLine($glowCyan, 126, 340, 126, 205)
$graphics.DrawLine($glowCyan, 126, 205, 244, 205)
$graphics.DrawLine($cyanPen, 126, 340, 126, 205)
$graphics.DrawLine($cyanPen, 126, 205, 244, 205)
$graphics.DrawLine($glowPink, 898, 684, 898, 819)
$graphics.DrawLine($glowPink, 898, 819, 780, 819)
$graphics.DrawLine($pinkPen, 898, 684, 898, 819)
$graphics.DrawLine($pinkPen, 898, 819, 780, 819)

$dataPen = New-Pen ([System.Drawing.Color]::FromArgb(86, 111, 238, 255)) 12
$dataPen.DashStyle = [System.Drawing.Drawing2D.DashStyle]::Dash
$dataPen.DashPattern = [single[]]@(1.1, 2.6)
foreach ($line in @(
  @(144, 468, 286, 468),
  @(738, 556, 880, 556),
  @(170, 612, 262, 612),
  @(762, 412, 854, 412)
)) {
  $graphics.DrawLine($dataPen, $line[0], $line[1], $line[2], $line[3])
}

$earPath = [System.Drawing.Drawing2D.GraphicsPath]::new()
$earPath.AddPolygon([System.Drawing.PointF[]]@(
  [System.Drawing.PointF]::new(238, 351),
  [System.Drawing.PointF]::new(268, 139),
  [System.Drawing.PointF]::new(435, 274),
  [System.Drawing.PointF]::new(589, 274),
  [System.Drawing.PointF]::new(756, 139),
  [System.Drawing.PointF]::new(786, 351),
  [System.Drawing.PointF]::new(806, 373),
  [System.Drawing.PointF]::new(806, 408),
  [System.Drawing.PointF]::new(806, 686),
  [System.Drawing.PointF]::new(806, 804),
  [System.Drawing.PointF]::new(688, 804),
  [System.Drawing.PointF]::new(336, 804),
  [System.Drawing.PointF]::new(218, 804),
  [System.Drawing.PointF]::new(218, 686),
  [System.Drawing.PointF]::new(218, 408),
  [System.Drawing.PointF]::new(218, 373)
))
$earBrush = New-Brush $pink $violet ([System.Drawing.RectangleF]::new(218, 139, 588, 665))
$graphics.FillPath($pinkGlowBrush, $earPath)
$graphics.FillPath($earBrush, $earPath)

$panelShadow = [System.Drawing.SolidBrush]::new([System.Drawing.Color]::FromArgb(100, 0, 2, 12))
$graphics.FillPath($panelShadow, (New-RoundedRectPath -Rect ([System.Drawing.RectangleF]::new(276, 293, 512, 486)) -Radius 116))

$panelPath = New-RoundedRectPath -Rect ([System.Drawing.RectangleF]::new(256, 273, 512, 486)) -Radius 116
$panelBrush = New-Brush `
  -Start ([System.Drawing.Color]::FromArgb(255, 10, 33, 72)) `
  -End ([System.Drawing.Color]::FromArgb(255, 11, 13, 43)) `
  -Rect ([System.Drawing.RectangleF]::new(256, 273, 512, 486))
$graphics.FillPath($panelBrush, $panelPath)
$graphics.DrawPath($glowCyan, $panelPath)
$panelPen = New-Pen $cyan 28
$graphics.DrawPath($panelPen, $panelPath)

$dividerPen = New-Pen ([System.Drawing.Color]::FromArgb(150, 125, 239, 255)) 12
$graphics.DrawLine($dividerPen, 260, 388, 764, 388)
$dotBrushes = @(
  [System.Drawing.SolidBrush]::new($pink),
  [System.Drawing.SolidBrush]::new($cyan),
  [System.Drawing.SolidBrush]::new($violet)
)
$graphics.FillEllipse($dotBrushes[0], 314, 317, 40, 40)
$graphics.FillEllipse($dotBrushes[1], 377, 317, 40, 40)
$graphics.FillEllipse($dotBrushes[2], 440, 317, 40, 40)

$promptPen = New-Pen $cyan 54
$graphics.DrawLines($glowCyan, [System.Drawing.PointF[]]@(
  [System.Drawing.PointF]::new(360, 487),
  [System.Drawing.PointF]::new(444, 574),
  [System.Drawing.PointF]::new(360, 661)
))
$graphics.DrawLines($promptPen, [System.Drawing.PointF[]]@(
  [System.Drawing.PointF]::new(360, 487),
  [System.Drawing.PointF]::new(444, 574),
  [System.Drawing.PointF]::new(360, 661)
))
$graphics.DrawLine($glowPink, 516, 664, 664, 664)
$underscorePen = New-Pen $pink 54
$graphics.DrawLine($underscorePen, 516, 664, 664, 664)

$knot = [System.Drawing.Drawing2D.GraphicsPath]::new()
$knot.AddPolygon([System.Drawing.PointF[]]@(
  [System.Drawing.PointF]::new(512, 836),
  [System.Drawing.PointF]::new(577, 884),
  [System.Drawing.PointF]::new(512, 932),
  [System.Drawing.PointF]::new(447, 884)
))
$graphics.FillPath($pinkGlowBrush, $knot)
$graphics.FillPath($earBrush, $knot)

$leftRibbon = [System.Drawing.Drawing2D.GraphicsPath]::new()
$leftRibbon.AddPolygon([System.Drawing.PointF[]]@(
  [System.Drawing.PointF]::new(420, 856),
  [System.Drawing.PointF]::new(487, 884),
  [System.Drawing.PointF]::new(438, 934),
  [System.Drawing.PointF]::new(351, 910)
))
$graphics.FillPath($earBrush, $leftRibbon)
$rightRibbon = [System.Drawing.Drawing2D.GraphicsPath]::new()
$rightRibbon.AddPolygon([System.Drawing.PointF[]]@(
  [System.Drawing.PointF]::new(604, 856),
  [System.Drawing.PointF]::new(537, 884),
  [System.Drawing.PointF]::new(586, 934),
  [System.Drawing.PointF]::new(673, 910)
))
$graphics.FillPath($earBrush, $rightRibbon)

$graphics.ResetClip()
$outerPath = New-RoundedRectPath -Rect ([System.Drawing.RectangleF]::new(44, 44, 936, 936)) -Radius 202
$outerPen = New-Pen $cyan 22
$graphics.DrawPath($outerPen, $outerPath)

$highlightPath = [System.Drawing.Drawing2D.GraphicsPath]::new()
$highlightPath.AddLine(208, 68, 790, 68)
$graphics.DrawPath((New-Pen $pink 18), $highlightPath)

$bitmap.Save($pngPath, [System.Drawing.Imaging.ImageFormat]::Png)

$sizes = @(256, 128, 64, 48, 32, 16)
$iconImages = foreach ($size in $sizes) {
  $scaled = [System.Drawing.Bitmap]::new(
    $size,
    $size,
    [System.Drawing.Imaging.PixelFormat]::Format32bppArgb
  )
  $scaledGraphics = [System.Drawing.Graphics]::FromImage($scaled)
  $scaledGraphics.Clear([System.Drawing.Color]::Transparent)
  $scaledGraphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
  $scaledGraphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
  $scaledGraphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
  $scaledGraphics.DrawImage($bitmap, 0, 0, $size, $size)
  $scaledGraphics.Dispose()
  $stream = [System.IO.MemoryStream]::new()
  $scaled.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
  $scaled.Dispose()
  [pscustomobject]@{
    Size = $size
    Bytes = $stream.ToArray()
  }
  $stream.Dispose()
}

$fileStream = [System.IO.File]::Open($icoPath, [System.IO.FileMode]::Create)
$writer = [System.IO.BinaryWriter]::new($fileStream)
try {
  $writer.Write([uint16]0)
  $writer.Write([uint16]1)
  $writer.Write([uint16]$iconImages.Count)
  $offset = 6 + (16 * $iconImages.Count)
  foreach ($image in $iconImages) {
    $dimension = if ($image.Size -eq 256) { 0 } else { $image.Size }
    $writer.Write([byte]$dimension)
    $writer.Write([byte]$dimension)
    $writer.Write([byte]0)
    $writer.Write([byte]0)
    $writer.Write([uint16]1)
    $writer.Write([uint16]32)
    $writer.Write([uint32]$image.Bytes.Length)
    $writer.Write([uint32]$offset)
    $offset += $image.Bytes.Length
  }
  foreach ($image in $iconImages) {
    $writer.Write($image.Bytes)
  }
} finally {
  $writer.Dispose()
  $fileStream.Dispose()
}

$tileBrush.Dispose()
$earBrush.Dispose()
$panelBrush.Dispose()
$panelShadow.Dispose()
$pinkGlowBrush.Dispose()
foreach ($brush in $dotBrushes) { $brush.Dispose() }
foreach ($pen in @(
  $gridPen, $glowCyan, $glowPink, $cyanPen, $pinkPen, $dataPen,
  $dividerPen, $promptPen, $underscorePen, $outerPen
)) { $pen.Dispose() }
$graphics.Dispose()
$bitmap.Dispose()

Write-Output "Generated $pngPath"
Write-Output "Generated $icoPath"
