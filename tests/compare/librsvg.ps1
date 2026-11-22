param([string]$outdir)
# Renders resvg's test suite with librsvg (through vipsthumbnail), at the size of each reference:
# CMP_RESVG_TESTS is crates\resvg\tests\tests of a checkout of resvg, CMP_VIPS_BIN libvips' bin directory.
# Count what passes with: python passcount.py %CMP_RESVG_TESTS% outdir
$root = $env:CMP_RESVG_TESTS; $vth = "$env:CMP_VIPS_BIN\vipsthumbnail.exe"
Add-Type -AssemblyName System.Drawing
New-Item -ItemType Directory -Force $outdir | Out-Null
$n = 0
foreach ($f in Get-ChildItem $root -Recurse -Filter *.svg) {
  $rel = $f.FullName.Substring($root.Length + 1)
  $ref = $f.FullName -replace '\.svg$', '.png'
  if (-not (Test-Path $ref)) { continue }
  $img = [System.Drawing.Image]::FromFile($ref); $w = $img.Width; $h = $img.Height; $img.Dispose()
  $out = Join-Path $outdir (($rel -replace '[\\/]', '__') + '.png')
  $p = Start-Process -FilePath $vth -ArgumentList @("`"$($f.FullName)`"", '--size', "${w}x${h}!", '-o', "`"$out`"") -PassThru -WindowStyle Hidden
  try { $p.PriorityClass = 'BelowNormal'; $p.ProcessorAffinity = 0x3FF } catch {}
  if (-not $p.WaitForExit(20000)) { try { $p.Kill() } catch {} }
  $n++
}
"rendered $n"
