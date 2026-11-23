param([string]$only = '', [int]$n = 3, [int]$threads = 1)
# The rest of this package's features, the ones the rows of bench.ps1 leave
# out, each with the other tools that do the same: every format read and
# written, the named operations, the encoders' options, metadata, colour,
# watermarks, text, animation, tiles, PDF, batch and the inspection
# commands. On the photograph of 6000x4000 unless said otherwise, through
# runpeak: best wall time of $n runs, peak working set, peak private
# memory, and what went to temporary files (what the process wrote beyond
# its output). Lines read "group | feature | tool ...".
#   CMP_WORK     big420.jpg, base420rgb.png, anim.gif, logo.png (as bench.ps1)
#   CMP_MORE     the inputs mkmore.py and the lines below make, and out\
#   CMP_GI, CMP_RUNPEAK, CMP_VIPS_BIN, CMP_CWEBP, CMP_GIFSICLE, SHARP_DIR   as bench.ps1
$C = $env:CMP_WORK; $M = $env:CMP_MORE; $O = "$M\out"; $H = $PSScriptRoot
$rp = $env:CMP_RUNPEAK; $gi = $env:CMP_GI; $T = "$threads"
$vips = "$env:CMP_VIPS_BIN\vips.exe"; $vth = "$env:CMP_VIPS_BIN\vipsthumbnail.exe"; $vhead = "$env:CMP_VIPS_BIN\vipsheader.exe"
$cwebp = $env:CMP_CWEBP; $gifsicle = $env:CMP_GIFSICLE
$env:VIPS_CONCURRENCY = "$threads"; $env:LOGO = "$C\logo.png"
New-Item -ItemType Directory -Force $O | Out-Null
$jpg = "$C\big420.jpg"; $png = "$C\base420rgb.png"; $anim = "$C\anim.gif"; $logo = "$C\logo.png"
$I = "$M\in"
if (-not (Test-Path "$I\recomb.mat")) {
  # The inputs: those Pillow or a short script write (mkmore.py), and the
  # rest written by this package from the same photograph; libvips's masks.
  New-Item -ItemType Directory -Force $I | Out-Null
  python -I "$H\mkmore.py" $jpg $I
  foreach ($e in 'bmp', 'qoi', 'ppm', 'tga') { & $gi $jpg "$I\in.$e" }
  & $gi -tiff-compression deflate -tiff-predictor $jpg "$I\in_deflate.tif"
  & $gi -tiff-compression lzw -tiff-predictor $jpg "$I\in_lzw.tif"
  & $gi -tiff-compression jpeg -quality 85 $jpg "$I\in_jpeg.tif"
  & $gi -tiff-compression deflate -tiff-tile 256 -tiff-pyramid $jpg "$I\in_pyr.tif"
  & $gi -webp-lossless -webp-effort 1 $png "$I\in_ll.webp"
  & $gi -resize 256x256 -fit cover $jpg "$I\in.ico"
  & $gi -resize 1024x1024 -fit cover $jpg "$I\in.icns"
  & $gi -animated $anim "$I\anim.png"
  & $gi -animated -webp-effort 1 $anim "$I\anim.webp"
  & $gi -keep-exif -exif-artist Test -profile p3 $jpg "$I\p3.jpg"
  Set-Content "$I\sharpen.mat" "3 3 1 0`n-1 -1 -1`n-1 9 -1`n-1 -1 -1" -Encoding ascii
  Set-Content "$I\box5.mat" ("5 5`n" + (("255 255 255 255 255`n") * 5)) -Encoding ascii
  Set-Content "$I\recomb.mat" "3 3`n0.393 0.769 0.189`n0.349 0.686 0.168`n0.272 0.534 0.131" -Encoding ascii
}
$batch = @(Get-ChildItem "$I\batch\*.jpg" | ForEach-Object FullName)

# Row runs exe with a; out is its output file, or a directory (ending in \)
# made empty first that the output goes into.
function Row($group, $label, $out, $exe, $a) {
  if ($only -and "$group | $label" -notmatch $only) { return }
  if ($out) {
    if (Test-Path $out) { Remove-Item $out -Recurse -Force }
    if ($out.EndsWith('\')) { New-Item -ItemType Directory -Force $out | Out-Null }
  }
  $line = & $rp -n $n -label "$group | $label" $exe @a 2>&1 | Select-Object -Last 1
  $size = ''; $outBytes = 0
  if ($out -and (Test-Path $out)) {
    $outBytes = [int64](Get-ChildItem $out -Recurse -File | Measure-Object Length -Sum).Sum
    $size = '  out {0:N0}' -f $outBytes
  }
  if ($line -match 'wrote (\d+)') { $size += '  temp {0:N1}MB' -f (([int64]$matches[1] - $outBytes) / 1MB) }
  "$line$size"
}
$k = 0
function Next($tool, $ext) { $script:k++; if ($ext -eq '\') { return "$O\$tool$script:k\" } "$O\$tool$script:k.$ext" }
# This package: options, input, output.
function Ours($g, $f, $ext, $opts, $in = $jpg, $file = '') {
  if (-not $file) { $file = Next 'gi' $ext }
  $dst = $file; if ($ext -eq '\') { $dst = "$($file)t." + $(if ($opts -contains '-tile-layout') { 'dz' } else { 'dzi' }) }
  Row $g "$f | goimgtools" $file $gi (@('-threads', $T) + $opts + @($in, $dst))
}
function GiCmd($g, $f, $a, $out = '') { Row $g "$f | goimgtools" $out $gi $a }
function Sharp($g, $f, $task, $ext, $in = $jpg) {
  $file = Next 'sharp' $ext; $dst = $file; if ($ext -eq '\') { $dst = "$($file)t.dz" }
  Row $g "$f | sharp" $file 'node' @("$H\more_sharp.js", $task, $in, $dst, $T)
}
function Pil($g, $f, $task, $ext, $in = $jpg) {
  $file = if ($ext) { Next 'pil' $ext } else { '' }
  Row $g "$f | Pillow" $file 'python' @('-I', "$H\more_pil.py", $task, $in, $(if ($file) { $file } else { 'none' }))
}
# libvips: its arguments, %OUT% for the output; suffix the options of the saver.
function Vips($g, $f, $ext, $a, $suffix = '') {
  $file = Next 'vips' $ext; $dst = $file; if ($ext -eq '\') { $dst = "$($file)t" }
  Row $g "$f | libvips" $file $vips ($a | ForEach-Object { $_ -replace '%OUT%', "$dst$suffix" })
}
function Vth($g, $f, $in, $opts) { $file = Next 'vth' 'jpg'; Row $g "$f | vipsthumbnail" $file $vth (@($in) + $opts + @('-o', "$file[Q=85]")) }
function Other($g, $f, $tool, $ext, $exe, $a) { $file = Next $tool $ext; Row $g "$f | $tool" $file $exe ($a | ForEach-Object { $_ -replace '%OUT%', $file }) }

$q = @('-quality', '85')
$vq = '[Q=85]'

# Reading: each format to a JPEG of 1536 wide.
$g = 'read'
foreach ($f in 'in.bmp', 'in.qoi', 'in.ppm', 'in.tga', 'in.psd', 'in.dds', 'in.fits', 'in.pcx', 'in.sgi', 'in.xbm', 'in.xpm',
  'in_deflate.tif', 'in_lzw.tif', 'in_jpeg.tif', 'in_pyr.tif', 'in_ll.webp', 'in.ico', 'in.icns', 'in.svg') {
  Ours $g $f 'jpg' (@('-resize', '1536x') + $q) "$I\$f"
  if ($f -match '\.(tif|webp|svg)$') { Vth $g $f "$I\$f" @('-s', '1536x'); Sharp $g $f 'thumb' 'jpg' "$I\$f" }
  if ($f -notmatch '\.svg$') { Pil $g $f 'thumb' 'jpg' "$I\$f" }
}
# A pyramid read from its nearest level, as vipsthumbnail always reads it.
Ours $g 'in_pyr.tif, -fast-shrink' 'jpg' (@('-fast-shrink', '-resize', '1536x') + $q) "$I\in_pyr.tif"
Vth $g 'in_pyr.tif, -fast-shrink' "$I\in_pyr.tif" @('-s', '1536x')
Ours $g 'in.svg to PNG, same size' 'png' @('-png-level', '1') "$I\in.svg"
Vips $g 'in.svg to PNG, same size' 'png' @('copy', "$I\in.svg", '%OUT%') '[compression=1]'

# Writing: the photograph in each format, same size.
$g = 'write'
foreach ($e in 'bmp', 'qoi', 'ppm', 'tga') { Ours $g $e $e @(); Pil $g $e $e $e }
Ours $g 'pdf' 'pdf' @()
Ours $g 'pdf quality 85' 'pdf' $q; Pil $g 'pdf quality 85' 'pdf85' 'pdf'
$tiffs = [ordered]@{
  'tiff none'              = @(@(), 'tiff_none', '[compression=none]')
  'tiff deflate predictor' = @(@('-tiff-compression', 'deflate', '-tiff-predictor'), 'tiff_deflate', '[compression=deflate,predictor=horizontal]')
  'tiff lzw predictor'     = @(@('-tiff-compression', 'lzw', '-tiff-predictor'), 'tiff_lzw', '[compression=lzw,predictor=horizontal]')
  'tiff jpeg'              = @(@('-tiff-compression', 'jpeg', '-quality', '85'), 'tiff_jpeg', '[compression=jpeg,Q=85]')
  'tiff tiles 256'         = @(@('-tiff-compression', 'deflate', '-tiff-tile', '256'), 'tiff_tile', '[compression=deflate,tile,tile-width=256,tile-height=256]')
  'tiff pyramid'           = @(@('-tiff-compression', 'deflate', '-tiff-tile', '256', '-tiff-pyramid'), 'tiff_pyramid', '[compression=deflate,tile,tile-width=256,tile-height=256,pyramid]')
}
foreach ($key in $tiffs.Keys) {
  $sp = $tiffs[$key]
  Ours $g $key 'tif' $sp[0]
  Vips $g $key 'tif' @('copy', "$jpg[access=sequential]", '%OUT%') $sp[2]
  Sharp $g $key $sp[1] 'tif'
  if ($key -notmatch 'tiles|pyramid') { Pil $g $key $sp[1] 'tif' }
}
Ours $g 'ico 256' 'ico' @('-resize', '256x256', '-fit', 'cover')
Ours $g 'ico 16,32,48,256' 'ico' @('-ico-sizes', '16,32,48,256'); Pil $g 'ico 16,32,48,256' 'ico4' 'ico'
Ours $g 'icns standard sizes' 'icns' @('-resize', '1024x1024', '-fit', 'cover'); Pil $g 'icns standard sizes' 'icns' 'icns'
foreach ($l in 1, 4, 9) {
  Ours $g "png level $l" 'png' @('-png-level', "$l")
  Vips $g "png level $l" 'png' @('copy', "$jpg[access=sequential]", '%OUT%') "[compression=$l]"
  Sharp $g "png level $l" "png$l" 'png'
  Pil $g "png level $l" "png$l" 'png'
}
Other $g 'png level 9' 'libvips filter=all' 'png' $vips @('copy', "$jpg[access=sequential]", '%OUT%[compression=9,filter=all]')
Ours $g 'png 256 colours' 'png' @('-png-colors', '256')
Vips $g 'png 256 colours' 'png' @('copy', "$jpg[access=sequential]", '%OUT%') '[palette]'
Sharp $g 'png 256 colours' 'png256' 'png'
Pil $g 'png 256 colours' 'png256' 'png'
Ours $g 'png level 10, 1536 wide' 'png' @('-png-level', '10', '-resize', '1536x')
Ours $g 'gif same size' 'gif' @()
Vips $g 'gif same size' 'gif' @('copy', "$jpg[access=sequential]", '%OUT%')
Sharp $g 'gif same size' 'gif' 'gif'
Pil $g 'gif same size' 'gif' 'gif'
Ours $g 'gif lossy 24, 1536 wide' 'gif' @('-gif-lossy', '24', '-resize', '1536x')
Ours $g 'webp near-lossless 60' 'webp' @('-webp-near-lossless', '-quality', '60') $png
Other $g 'webp near-lossless 60' 'cwebp' 'webp' $cwebp @('-quiet', '-near_lossless', '60', $png, '-o', '%OUT%')
Vips $g 'webp near-lossless 60' 'webp' @('copy', "$png[access=sequential]", '%OUT%') '[near_lossless,Q=60]'
Sharp $g 'webp near-lossless 60' 'webp_nl60' 'webp' $png
Ours $g 'webp lossless effort 9' 'webp' @('-webp-lossless', '-webp-effort', '9') $png
Other $g 'webp lossless effort 9' 'cwebp' 'webp' $cwebp @('-quiet', '-lossless', '-z', '9', $png, '-o', '%OUT%')
Sharp $g 'webp lossless effort 9' 'webp_ll6' 'webp' $png
$jopts = [ordered]@{
  'jpeg progressive'          = @(@('-jpeg-progressive'), 'jpg_prog', ',interlace', 'jpg_prog')
  'jpeg optimize'             = @(@('-jpeg-optimize'), 'jpg_opt', ',optimize_coding', 'jpg_opt')
  'jpeg trellis'              = @(@('-jpeg-trellis'), 'jpg_trellis', ',trellis_quant', '')
  'jpeg progressive trellis jpegli tables' = @(@('-jpeg-progressive', '-jpeg-trellis', '-jpeg-quant-table', '9'), 'jpg_moz', ',interlace,trellis_quant,optimize_scans,quant_table=3', '')
  'jpeg 4:4:4'                = @(@('-jpeg-chroma', '444'), 'jpg_444', ',subsample_mode=off', 'jpg_444')
  'jpeg overshoot deringing'  = @(@('-jpeg-overshoot-deringing'), 'jpg_dering', ',overshoot_deringing', '')
}
foreach ($j in $jopts.Keys) {
  $sp = $jopts[$j]
  Ours $g $j 'jpg' ($q + $sp[0])
  Vips $g $j 'jpg' @('copy', "$jpg[access=sequential]", '%OUT%') "[Q=85$($sp[2])]"
  Sharp $g $j $sp[1] 'jpg'
  if ($sp[3]) { Pil $g $j $sp[3] 'jpg' }
}
Ours $g 'jpeg -max-size 1MB' 'jpg' @('-max-size', '1MB')
Ours $g 'jpeg -min-ssim 0.98' 'jpg' @('-min-ssim', '0.98')
foreach ($key in @(@('deep zoom tiles', @(), @(), 'dz'), @('google tiles', @('-tile-layout', 'google'), @('--layout', 'google'), 'dz_google'), @('zoomify tiles', @('-tile-layout', 'zoomify'), @('--layout', 'zoomify'), 'dz_zoomify'))) {
  Ours $g $key[0] '\' $key[1]
  Vips $g $key[0] '\' (@('dzsave', $jpg, '%OUT%', '--tile-size', '256') + $key[2])
  Sharp $g $key[0] $key[3] '\'
}

# The options of the single-file mode, JPEG in and out.
$g = 'option'
function Vop($f, $a) { Vips $g $f 'jpg' $a $vq }
Ours $g 'crop 3000x2000' 'jpg' (@('-crop', '1500,1000,3000,2000') + $q); Vop 'crop 3000x2000' @('crop', $jpg, '%OUT%', '1500', '1000', '3000', '2000'); Sharp $g 'crop 3000x2000' 'crop' 'jpg'; Pil $g 'crop 3000x2000' 'crop' 'jpg'
Ours $g 'trim' 'jpg' (@('-trim', '10') + $q); Sharp $g 'trim' 'trim' 'jpg'; Pil $g 'trim' 'trim' 'jpg'
Ours $g 'extend 100 mirror' 'jpg' (@('-extend', '100,100,100,100', '-extend-with', 'mirror') + $q); Vop 'extend 100 mirror' @('embed', $jpg, '%OUT%', '100', '100', '6200', '4200', '--extend', 'mirror'); Sharp $g 'extend 100 mirror' 'extend_mirror' 'jpg'
Ours $g 'flip' 'jpg' (@('-flip') + $q); Vop 'flip' @('flip', $jpg, '%OUT%', 'vertical'); Sharp $g 'flip' 'flip' 'jpg'; Pil $g 'flip' 'flip' 'jpg'
Ours $g 'flop' 'jpg' (@('-flop') + $q); Vop 'flop' @('flip', "$jpg[access=sequential]", '%OUT%', 'horizontal'); Sharp $g 'flop' 'flop' 'jpg'; Pil $g 'flop' 'flop' 'jpg'
Ours $g 'rotate 180' 'jpg' (@('-rotate', '180') + $q); Vop 'rotate 180' @('rot', $jpg, '%OUT%', 'd180'); Sharp $g 'rotate 180' 'rot180' 'jpg'; Pil $g 'rotate 180' 'rot180' 'jpg'
Ours $g 'resize 1536 nearest' 'jpg' (@('-resize', '1536x', '-kernel', 'nearest') + $q); Vop 'resize 1536 nearest' @('resize', "$jpg[access=sequential]", '%OUT%', '0.256', '--kernel', 'nearest'); Sharp $g 'resize 1536 nearest' 'nearest' 'jpg'; Pil $g 'resize 1536 nearest' 'nearest' 'jpg'
Ours $g 'resize 1536 libvips method' 'jpg' (@('-resize', '1536x', '-resize-method', 'libvips') + $q)
Ours $g 'resize 1536 pillow method' 'jpg' (@('-resize', '1536x', '-resize-method', 'pillow') + $q)
Ours $g 'resize 1536 linear light' 'jpg' (@('-resize', '1536x', '-linear-light') + $q); Vth $g 'resize 1536 linear light' $jpg @('-s', '1536x', '--linear')
Ours $g 'resize 1536x1536 cover attention' 'jpg' (@('-resize', '1536x1536', '-fit', 'cover', '-position', 'attention') + $q); Vth $g 'resize 1536x1536 cover attention' $jpg @('-s', '1536x1536', '--smartcrop', 'attention'); Sharp $g 'resize 1536x1536 cover attention' 'attention' 'jpg'
Ours $g 'resize 1536x1536 cover entropy' 'jpg' (@('-resize', '1536x1536', '-fit', 'cover', '-position', 'entropy') + $q); Vth $g 'resize 1536x1536 cover entropy' $jpg @('-s', '1536x1536', '--smartcrop', 'entropy'); Sharp $g 'resize 1536x1536 cover entropy' 'entropy' 'jpg'
Ours $g 'resize 1536x1536 contain' 'jpg' (@('-resize', '1536x1536', '-fit', 'contain', '-background', 'white') + $q); Sharp $g 'resize 1536x1536 contain' 'contain' 'jpg'; Pil $g 'resize 1536x1536 contain' 'contain' 'jpg'
Ours $g 'enlarge to 12000 wide' 'jpg' (@('-resize', '12000x') + $q); Vop 'enlarge to 12000 wide' @('resize', "$jpg[access=sequential]", '%OUT%', '2'); Sharp $g 'enlarge to 12000 wide' 'enlarge' 'jpg'; Pil $g 'enlarge to 12000 wide' 'enlarge' 'jpg'
Ours $g 'grayscale linear' 'jpg' (@('-grayscale-linear') + $q); Sharp $g 'grayscale linear' 'grey' 'jpg'
Ours $g 'sharpen mild' 'jpg' (@('-sharpen', 'mild') + $q)
Ours $g 'sharpen libvips' 'jpg' (@('-sharpen', '2,libvips') + $q); Vop 'sharpen libvips' @('sharpen', "$jpg[access=sequential]", '%OUT%', '--sigma', '2'); Sharp $g 'sharpen libvips' 'sharpen' 'jpg'
Ours $g 'blur 10' 'jpg' (@('-blur', '10') + $q); Vop 'blur 10' @('gaussblur', "$jpg[access=sequential]", '%OUT%', '10'); Sharp $g 'blur 10' 'blur10' 'jpg'; Pil $g 'blur 10' 'blur10' 'jpg'
Ours $g 'lut warm 9' 'jpg' (@('-lut', "$H\..\testdata\lut\warm_9.cube") + $q)
Ours $g 'lut 33' 'jpg' (@('-lut', "$I\warm_33.cube") + $q); $env:CUBE = "$I\warm_33.cube"; Pil $g 'lut 33' 'lut33' 'jpg'
Ours $g 'srgb from Display P3' 'jpg' (@('-srgb') + $q) "$I\p3.jpg"; Vop 'srgb from Display P3' @('icc_transform', "$I\p3.jpg", '%OUT%', 'srgb'); Sharp $g 'srgb from Display P3' 'srgb' 'jpg' "$I\p3.jpg"; Pil $g 'srgb from Display P3' 'srgb' 'jpg' "$I\p3.jpg"
Ours $g 'profile adobe-rgb' 'jpg' (@('-profile', 'adobe-rgb') + $q)
Ours $g 'keep exif xmp iptc' 'jpg' (@('-keep-exif', '-keep-xmp', '-keep-iptc') + $q) "$I\p3.jpg"; Vop 'keep exif xmp iptc' @('copy', "$I\p3.jpg[access=sequential]", '%OUT%'); Sharp $g 'keep exif xmp iptc' 'keepmeta' 'jpg' "$I\p3.jpg"; Pil $g 'keep exif xmp iptc' 'keepmeta' 'jpg' "$I\p3.jpg"
Ours $g 'exif fields' 'jpg' (@('-exif-artist', 'A', '-exif-copyright', 'B', '-exif-description', 'C') + $q)

# The named operations (-op), JPEG in and out (those that make transparency to TIFF).
$g = 'op'
# Each: the operation; sharp's task; Pillow's task; libvips's arguments.
$ops = @(
  @('affine:0.966,-0.259,0.259,0.966,white', 'affine', 'affine', @('affine', $jpg, '%OUT%', '0.966 -0.259 0.259 0.966', '--background', '255 255 255')),
  @('arc:3000,2000,1500,1000,30,300,stroke=red,width=12', '', 'arc', $null),
  @('autocontrast:2', '', 'autocontrast', $null),
  @('bandbool:and', 'bandbool', '', @('bandbool', "$jpg[access=sequential]", '%OUT%', 'and')),
  @('boxblur:5', '', 'boxblur', $null),
  @('chord:3000,2000,1500,1000,30,300,fill=red', '', 'chord', $null),
  @('circle:3000,2000,1500,fill=#ff000080,stroke=blue,width=20', '', 'circle', $null),
  @('clahe:64,64', 'clahe', '', @('hist_local', $jpg, '%OUT%', '64', '64')),
  @('convolve:3,3,-1,-1,-1,-1,9,-1,-1,-1,-1', 'convolve', 'convolve', @('conv', "$jpg[access=sequential]", '%OUT%', "$I\sharpen.mat")),
  @('crop:1000,1000,4000,2000', '', '', $null),
  @('curves:64:40,192:220', '', '', $null),
  @('dilate:2', 'dilate', 'dilate', @('morph', "$jpg[access=sequential]", '%OUT%', "$I\box5.mat", 'dilate')),
  @('ellipse:3000,2000,2500,1500,fill=yellow,stroke=black,width=30', '', 'ellipse', $null),
  @('enhance:color,1.5', '', 'enhance_color', $null),
  @('enhance:contrast,1.3', '', 'enhance_contrast', $null),
  @('enhance:brightness,1.2', '', 'enhance_brightness', $null),
  @('enhance:sharpness,2', '', 'enhance_sharpness', $null),
  @('equalize', '', 'equalize', @('hist_equal', $jpg, '%OUT%')),
  @('erode:2', 'erode', 'erode', @('morph', "$jpg[access=sequential]", '%OUT%', "$I\box5.mat", 'erode')),
  @('extend:200,200,200,200,mirror', '', '', $null),
  @('extract-channel:green', 'extract_green', 'extract_green', @('extract_band', "$jpg[access=sequential]", '%OUT%', '1')),
  @('filter:blur', '', 'filter_blur', $null), @('filter:contour', '', 'filter_contour', $null), @('filter:detail', '', 'filter_detail', $null),
  @('filter:edge-enhance', '', 'filter_edge-enhance', $null), @('filter:edge-enhance-more', '', 'filter_edge-enhance-more', $null),
  @('filter:emboss', '', 'filter_emboss', $null), @('filter:find-edges', '', 'filter_find-edges', $null), @('filter:sharpen', '', 'filter_sharpen', $null),
  @('filter:smooth', '', 'filter_smooth', $null), @('filter:smooth-more', '', 'filter_smooth-more', $null),
  @('flatten:white', '', '', $null), @('flip', '', '', $null), @('flop', '', '', $null),
  @('gamma:2.2', 'gamma', 'gamma', @('gamma', "$jpg[access=sequential]", '%OUT%', '--exponent', '2.2')),
  @('grayscale', '', '', $null), @('grayscale:linear', '', '', $null),
  @('line:100,100,5900,3900,100,3900,stroke=red,width=25', '', 'line', $null),
  @('linear:1.2,-20', 'linear', 'linear', @('linear', "$jpg[access=sequential]", '%OUT%', '--', '1.2', '-20')),
  @('median:3', 'median3', 'median3', @('rank', "$jpg[access=sequential]", '%OUT%', '3', '3', '4')),
  @('median:7', 'median7', 'median7', @('rank', "$jpg[access=sequential]", '%OUT%', '7', '7', '24')),
  @('modulate:1.1,1.3,20', '', '', $null),
  @('negate', 'negate', 'negate', @('invert', "$jpg[access=sequential]", '%OUT%')),
  @('normalize', 'normalise', '', $null),
  @('pie:3000,2000,1500,1000,30,300,fill=red', '', 'pie', $null),
  @('polygon:500,500,5500,800,4000,3500,800,3000,fill=#00ff0080,stroke=black,width=10', '', 'polygon', $null),
  @('posterize:3', '', 'posterize', $null),
  @('rank:5,12', '', 'rank', @('rank', "$jpg[access=sequential]", '%OUT%', '5', '5', '12')),
  @('recomb:0.393,0.769,0.189,0.349,0.686,0.168,0.272,0.534,0.131', 'recomb', 'recomb', @('recomb', "$jpg[access=sequential]", '%OUT%', "$I\recomb.mat")),
  @('rect:500,500,5000,3000,fill=#0000ff60,radius=200', '', 'rect', $null),
  @('resize:1536,0', '', '', $null),
  @('rotate:15,white,pillow,bicubic,expand', '', 'rotate15', $null),
  @('sharpen:2,libvips', '', '', $null),
  @('solarize', '', 'solarize', $null),
  @('threshold', 'threshold', 'threshold', $null),
  @('threshold:128,channels', '', '', $null),
  @('tint:#704214', 'tint', 'tint', $null),
  @('transform:6000,4000,perspective,1,0.1,0,0.05,1,0,0.00002,0.00001', '', 'perspective', $null),
  @('trim', '', '', $null),
  @('blur:3,libvips', 'blur3', '', @('gaussblur', "$jpg[access=sequential]", '%OUT%', '3'))
)
foreach ($sp in $ops) {
  Ours $g $sp[0] 'jpg' (@('-op', $sp[0]) + $q)
  if ($sp[3]) { Vop $sp[0] $sp[3] }
  if ($sp[1]) { Sharp $g $sp[0] $sp[1] 'jpg' }
  if ($sp[2]) { Pil $g $sp[0] $sp[2] 'jpg' }
}
foreach ($sp in @(@('ensure-alpha:0.5', 'ensure_alpha', 'ensure_alpha'), @('unflatten', 'unflatten', ''), @('remove-alpha', 'remove_alpha', 'remove_alpha'), @('negate:alpha', 'negate_alpha', ''))) {
  Ours $g $sp[0] 'tif' @('-op', $sp[0])
  Sharp $g $sp[0] $sp[1] 'tif'
  if ($sp[2]) { Pil $g $sp[0] $sp[2] 'tif' }
}

# Watermarks, text and the invisible mark.
$g = 'mark'
Ours $g 'logo, southeast' 'jpg' (@('-watermark', $logo) + $q)
Ours $g 'logo tiled, 30 degrees, opacity 0.3' 'jpg' (@('-watermark', $logo, '-watermark-tile', '-watermark-angle', '30', '-watermark-opacity', '0.3') + $q); Sharp $g 'logo tiled, 30 degrees, opacity 0.3' 'logo_tiled' 'jpg'; Pil $g 'logo tiled, 30 degrees, opacity 0.3' 'logo_tiled' 'jpg'
Ours $g 'logo 20% wide, multiply' 'jpg' (@('-watermark', $logo, '-watermark-width', '20%', '-watermark-blend', 'multiply') + $q)
Ours $g 'text watermark' 'jpg' (@('-watermark-text', 'Copyright 2026', '-font-size', '120', '-text-color', '#ffffffa0', '-stroke-width', '3', '-shadow-color', 'black', '-shadow-blur', '6') + $q); Sharp $g 'text watermark' 'text_mark' 'jpg'; Pil $g 'text watermark' 'text_mark' 'jpg'
Ours $g 'text watermark tiled' 'jpg' (@('-watermark-text', 'SAMPLE', '-font-size', '96', '-watermark-tile', '-watermark-angle', '-30', '-watermark-opacity', '0.25') + $q)
Ours $g 'invisible mark, blind' 'jpg' (@('-mark', 'user-12345', '-mark-key', 'k') + $q) $jpg "$O\marked.jpg"
Ours $g 'invisible mark, screen' 'jpg' (@('-mark', 'user-12345', '-mark-key', 'k', '-mark-kind', 'screen') + $q)
$text = 'The quick brown fox jumps over the lazy dog. ' * 40
GiCmd $g 'text image, wrapped' @('-text', $text, '-font-size', '48', '-text-width', '2000', '-text-background', 'white', "$O\text.png") "$O\text.png"
Sharp $g 'text image, wrapped' 'text_image' 'png'; Pil $g 'text image, wrapped' 'text_image' 'png'

# Animation.
$g = 'anim'
Ours $g 'APNG to GIF' 'gif' @('-animated') "$I\anim.png"; Pil $g 'APNG to GIF' 'anim_gif' 'gif' "$I\anim.png"
Ours $g 'GIF to APNG' 'png' @('-animated') $anim; Pil $g 'GIF to APNG' 'anim_apng' 'png' $anim
Ours $g 'animated WebP to GIF' 'gif' @('-animated') "$I\anim.webp"; Vips $g 'animated WebP to GIF' 'gif' @('copy', "$I\anim.webp[n=-1]", '%OUT%'); Sharp $g 'animated WebP to GIF' 'anim_gif' 'gif' "$I\anim.webp"; Pil $g 'animated WebP to GIF' 'anim_gif' 'gif' "$I\anim.webp"
Ours $g 'GIF lossy 24' 'gif' @('-animated', '-gif-lossy', '24') $anim; Other $g 'GIF lossy 24' 'gifsicle' 'gif' $gifsicle @('-O3', '--lossy=24', $anim, '-o', '%OUT%')
Ours $g 'GIF effort 1, frame delay 50' 'gif' @('-animated', '-gif-effort', '1', '-frame-delay', '50') $anim; Other $g 'GIF effort 1, frame delay 50' 'gifsicle' 'gif' $gifsicle @('--delay', '5', $anim, '-o', '%OUT%')
Ours $g 'GIF watermark' 'gif' @('-animated', '-watermark', $logo) $anim

# The commands that only look.
$g = 'inspect'
GiCmd $g 'info' @('info', $jpg); Row $g 'info | vipsheader' '' $vhead @($jpg); Sharp $g 'info' 'info' ''; Pil $g 'info' 'info' ''
GiCmd $g 'info, PSD' @('info', '-json', "$I\in.psd"); Pil $g 'info, PSD' 'info' '' "$I\in.psd"
GiCmd $g 'check JPEG' @('check', $jpg); Row $g 'check JPEG | libvips avg' '' $vips @('avg', "$jpg[access=sequential]"); Pil $g 'check JPEG' 'check' ''
GiCmd $g 'check PNG' @('check', $png); Row $g 'check PNG | libvips avg' '' $vips @('avg', "$png[access=sequential]"); Pil $g 'check PNG' 'check' '' $png
GiCmd $g 'compare' @('compare', $jpg, "$I\p3.jpg")
GiCmd $g 'stats' @('stats', $jpg); Row $g 'stats | libvips' '' $vips @('stats', "$jpg[access=sequential]", "$O\vstats.mat"); Sharp $g 'stats' 'stats' ''; Pil $g 'stats' 'stats' ''
GiCmd $g 'hash' @('hash', $jpg)
GiCmd $g 'placeholder' @('placeholder', $jpg)
GiCmd $g 'exif' @('exif', "$I\p3.jpg"); Pil $g 'exif' 'exif' '' "$I\p3.jpg"
GiCmd $g 'readmark blind' @('readmark', '-key', 'k', "$O\marked.jpg")
GiCmd $g 'readmark, no mark' @('readmark', '-key', 'k', $jpg)
GiCmd $g 'pages: 3 photographs to PDF' @('pages', $jpg, "$I\p3.jpg", $jpg, "$O\pages.pdf") "$O\pages.pdf"
Row $g 'pages: 3 photographs to PDF | Pillow' "$O\pilpages.pdf" 'python' @('-I', "$H\more_pil.py", 'pages', "$jpg;$I\p3.jpg;$jpg", "$O\pilpages.pdf")
GiCmd $g 'batch: 10 photographs to 1536 wide' @('batch', '-overwrite', '-resize', '1536x', '-quality', '85', "$I\batch", "$O\batch\") "$O\batch\"
Row $g 'batch: 10 photographs to 1536 wide | vipsthumbnail' "$O\vbatch\" $vth ($batch + @('-s', '1536x', '-o', "$O\vbatch\%s.jpg[Q=85]"))
Row $g 'batch: 10 photographs to 1536 wide | sharp' "$O\sbatch\" 'node' @("$H\more_sharp.js", 'batch', "$I\batch", "$O\sbatch", $T)
Row $g 'batch: 10 photographs to 1536 wide | Pillow' "$O\pbatch\" 'python' @('-I', "$H\more_pil.py", 'batch', "$I\batch", "$O\pbatch")
GiCmd $g 'batch -jobs 1' @('batch', '-overwrite', '-jobs', '1', '-resize', '1536x', '-quality', '85', "$I\batch", "$O\batch\") "$O\batch\"
