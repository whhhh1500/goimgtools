param([string]$only = '', [int]$n = 3, [int]$threads = 1)
# Rows of docs/comparison.md: every tool does the same task on the same input; the best
# wall time of $n runs and the largest peak working set (see README.md for what it needs).
#   CMP_WORK      a directory holding the inputs (big420.jpg, base420rgb.png, big80.webp,
#                 big24.gif, t.tif, anim.gif, logo.png) and taking the outputs (out\)
#   CMP_GI        goimgtools' command (go build ./cmd/goimgtools)
#   CMP_RUNPEAK   runpeak (go build ./tests/compare/runpeak)
#   CMP_VIPS_BIN  the bin directory of libvips (vips.exe, vipsthumbnail.exe)
#   CMP_CWEBP, CMP_DWEBP, CMP_GIFSICLE   those programs
#   SHARP_DIR     node_modules\sharp
$C = $env:CMP_WORK; $O = "$C\out"; $H = $PSScriptRoot
New-Item -ItemType Directory -Force $O | Out-Null
$rp = $env:CMP_RUNPEAK; $gi = $env:CMP_GI
$vips = "$env:CMP_VIPS_BIN\vips.exe"; $vth = "$env:CMP_VIPS_BIN\vipsthumbnail.exe"
$cwebp = $env:CMP_CWEBP; $dwebp = $env:CMP_DWEBP; $gifsicle = $env:CMP_GIFSICLE
$env:VIPS_CONCURRENCY = "$threads"; $env:LOGO = "$C\logo.png"
$jpg = "$C\big420.jpg"; $png = "$C\base420rgb.png"; $webp = "$C\big80.webp"; $gif = "$C\big24.gif"; $tif = "$C\t.tif"; $anim = "$C\anim.gif"
$T = "$threads"
function Row($group, $label, $out, $exe, $a) {
  if ($only -and $group -notmatch $only) { return }
  if ($out -and (Test-Path $out)) { Remove-Item $out -Force }
  $line = & $rp -n $n -label "$group | $label" $exe @a 2>&1 | Select-Object -Last 1
  $size = ''
  if ($out -and (Test-Path $out)) { $size = '  out {0:N0}' -f (Get-Item $out).Length }
  "$line$size"
}
function Sharp($group, $task, $in, $ext, $label = 'sharp') { $o = "$O\sharp_$($group -replace '\W','')_$task.$ext"; Row $group $label $o 'node' @("$H\bench_sharp.js", $task, $in, $o, $T) }
function Pil($group, $task, $in, $ext, $label = 'Pillow') { $o = "$O\pil_$($group -replace '\W','')_$task.$ext"; Row $group $label $o 'python' @("$H\bench_pil.py", $task, $in, $o) }
function Ours($group, $label, $ext, $a, $in) { $o = "$O\ours_$($group -replace '\W','')_$($label -replace '\W','').$ext"; Row $group $label $o $gi (@('-threads', $T) + $a + @($in, $o)) }
function Vips($group, $label, $ext, $a, $suffix = '') { $o = "$O\vips_$($group -replace '\W','')_$($label -replace '\W','').$ext"; $a2 = $a | ForEach-Object { $_ -replace '%OUT%', "$o$suffix" }; Row $group $label $o $vips $a2 }

# Start-up footprints.
Row 'base' 'goimgtools formats' '' $gi @('formats')
Row 'base' 'vips --version' '' $vips @('--version')
Row 'base' 'node sharp (load only)' '' 'node' @('-e', "require(process.env.SHARP_DIR)")
Row 'base' 'python Pillow (load only)' '' 'python' @('-c', 'from PIL import Image')

# JPEG 6000x4000 -> JPEG 1536 wide.
$g = 'jpg-thumb'
Ours $g 'goimgtools' 'jpg' @('-resize', '1536x', '-kernel', 'lanczos3', '-quality', '85') $jpg
Ours $g 'goimgtools fast-shrink' 'jpg' @('-fast-shrink', '-resize', '1536x', '-kernel', 'lanczos3', '-quality', '85') $jpg
Row $g 'vipsthumbnail' "$O\vips_jpgthumb.jpg" $vth @($jpg, '--size', '1536x', '-o', "$O\vips_jpgthumb.jpg[Q=85]")
Sharp $g 'thumb' $jpg 'jpg' 'sharp'
Sharp $g 'thumbslow' $jpg 'jpg' 'sharp fastShrinkOnLoad=false'
Pil $g 'thumbdraft' $jpg 'jpg' 'Pillow draft+resize'
Pil $g 'thumb' $jpg 'jpg' 'Pillow resize'

# JPEG -> JPEG, same size.
$g = 'jpg-copy'
Ours $g 'goimgtools' 'jpg' @('-quality', '85') $jpg
Vips $g 'vips copy' 'jpg' @('copy', "$jpg[access=sequential]", '%OUT%') '[Q=85]'
Sharp $g 'copy' $jpg 'jpg'
Pil $g 'copy' $jpg 'jpg'

# PNG 6000x4000 -> PNG 1536 wide.
$g = 'png-thumb'
Ours $g 'goimgtools' 'png' @('-resize', '1536x', '-kernel', 'lanczos3') $png
Row $g 'vipsthumbnail' "$O\vips_pngthumb.png" $vth @($png, '--size', '1536x', '-o', "$O\vips_pngthumb.png")
Sharp $g 'thumb' $png 'png'
Pil $g 'thumb' $png 'png'

# PNG -> PNG, same size (each at its default compression).
$g = 'png-copy'
Ours $g 'goimgtools' 'png' @() $png
Vips $g 'vips copy' 'png' @('copy', "$png[access=sequential]", '%OUT%')
Sharp $g 'copy' $png 'png'
Pil $g 'copy' $png 'png'

# PNG -> JPEG, same size (PNG decoding).
$g = 'png-jpg'
Ours $g 'goimgtools' 'jpg' @('-quality', '85') $png
Vips $g 'vips copy' 'jpg' @('copy', "$png[access=sequential]", '%OUT%') '[Q=85]'
Sharp $g 'copy' $png 'jpg'
Pil $g 'copy' $png 'jpg'

# JPEG -> lossy WebP quality 75, same size.
$g = 'webp-lossy'
Ours $g 'goimgtools' 'webp' @('-quality', '75') $jpg
Ours $g 'goimgtools effort 1' 'webp' @('-quality', '75', '-webp-effort', '1') $jpg
Vips $g 'vips copy' 'webp' @('copy', "$jpg[access=sequential]", '%OUT%') '[Q=75]'
Row $g 'cwebp' "$O\cwebp_lossy.webp" $cwebp @('-quiet', '-q', '75', $jpg, '-o', "$O\cwebp_lossy.webp")
Sharp $g 'copy' $jpg 'webp'
Pil $g 'copy' $jpg 'webp'

# PNG -> lossless WebP, same size.
$g = 'webp-lossless'
Ours $g 'goimgtools' 'webp' @('-webp-lossless') $png
Vips $g 'vips copy' 'webp' @('copy', "$png[access=sequential]", '%OUT%') '[lossless]'
Row $g 'cwebp' "$O\cwebp_ll.webp" $cwebp @('-quiet', '-lossless', $png, '-o', "$O\cwebp_ll.webp")
Sharp $g 'lossless' $png 'webp'
Pil $g 'lossless' $png 'webp'

# Lossy WebP 6000x4000 -> JPEG 1536 wide, and decoding alone.
$g = 'webp-thumb'
Ours $g 'goimgtools' 'jpg' @('-resize', '1536x', '-kernel', 'lanczos3', '-quality', '85') $webp
Ours $g 'goimgtools fast-shrink' 'jpg' @('-fast-shrink', '-resize', '1536x', '-kernel', 'lanczos3', '-quality', '85') $webp
Row $g 'vipsthumbnail' "$O\vips_webpthumb.jpg" $vth @($webp, '--size', '1536x', '-o', "$O\vips_webpthumb.jpg[Q=85]")
Sharp $g 'thumb' $webp 'jpg'
Pil $g 'thumb' $webp 'jpg'
$g = 'webp-decode'
Ours $g 'goimgtools to BMP' 'bmp' @() $webp
Row $g 'dwebp to BMP' "$O\dwebp.bmp" $dwebp @('-quiet', '-bmp', $webp, '-o', "$O\dwebp.bmp")
Vips $g 'vips copy to TIFF' 'tif' @('copy', "$webp[access=sequential]", '%OUT%')

# GIF and TIFF 6000x4000 -> JPEG 1536 wide.
$g = 'gif-thumb'
Ours $g 'goimgtools' 'jpg' @('-resize', '1536x', '-kernel', 'lanczos3', '-quality', '85') $gif
Row $g 'vipsthumbnail' "$O\vips_gifthumb.jpg" $vth @($gif, '--size', '1536x', '-o', "$O\vips_gifthumb.jpg[Q=85]")
Sharp $g 'thumb' $gif 'jpg'
Pil $g 'thumb' $gif 'jpg'
$g = 'tiff-thumb'
Ours $g 'goimgtools' 'jpg' @('-resize', '1536x', '-kernel', 'lanczos3', '-quality', '85') $tif
Row $g 'vipsthumbnail' "$O\vips_tifthumb.jpg" $vth @($tif, '--size', '1536x', '-o', "$O\vips_tifthumb.jpg[Q=85]")
Sharp $g 'thumb' $tif 'jpg'
Pil $g 'thumb' $tif 'jpg'

# JPEG -> GIF 1536 wide (a palette chosen for the picture).
$g = 'gif-out'
Ours $g 'goimgtools' 'gif' @('-resize', '1536x', '-kernel', 'lanczos3') $jpg
Sharp $g 'thumb' $jpg 'gif'
Pil $g 'thumb' $jpg 'gif'

# Operations on the 6000x4000 JPEG, JPEG out.
$g = 'op-blur'
Ours $g 'goimgtools' 'jpg' @('-blur', '2', '-quality', '85') $jpg
Vips $g 'vips gaussblur' 'jpg' @('gaussblur', "$jpg[access=sequential]", '%OUT%', '2') '[Q=85]'
Sharp $g 'blur' $jpg 'jpg'
Pil $g 'blur' $jpg 'jpg'
$g = 'op-sharpen'
Ours $g 'goimgtools' 'jpg' @('-sharpen', '2', '-quality', '85') $jpg
Vips $g 'vips sharpen' 'jpg' @('sharpen', "$jpg[access=sequential]", '%OUT%', '--sigma', '2') '[Q=85]'
Sharp $g 'sharpen' $jpg 'jpg'
Pil $g 'sharpen' $jpg 'jpg'
$g = 'op-rot90'
Ours $g 'goimgtools' 'jpg' @('-rotate', '90', '-quality', '85') $jpg
Vips $g 'vips rot' 'jpg' @('rot', $jpg, '%OUT%', 'd90') '[Q=85]'
Sharp $g 'rot90' $jpg 'jpg'
Pil $g 'rot90' $jpg 'jpg'
$g = 'op-rot30'
Ours $g 'goimgtools' 'jpg' @('-rotate', '30', '-background', 'white', '-quality', '85') $jpg
Vips $g 'vips rotate' 'jpg' @('rotate', $jpg, '%OUT%', '30', '--background', '255 255 255') '[Q=85]'
Sharp $g 'rot30' $jpg 'jpg'
Pil $g 'rot30' $jpg 'jpg'
$g = 'op-gray'
Ours $g 'goimgtools' 'jpg' @('-grayscale', '-quality', '85') $jpg
Vips $g 'vips colourspace' 'jpg' @('colourspace', "$jpg[access=sequential]", '%OUT%', 'b-w') '[Q=85]'
Sharp $g 'gray' $jpg 'jpg'
Pil $g 'gray' $jpg 'jpg'
$g = 'op-modulate'
Ours $g 'goimgtools' 'jpg' @('-modulate', '1.1,1.3,20', '-quality', '85') $jpg
Sharp $g 'modulate' $jpg 'jpg'
$g = 'op-composite'
Ours $g 'goimgtools' 'jpg' @('-watermark', "$C\logo.png", '-quality', '85') $jpg
Sharp $g 'composite' $jpg 'jpg'
Pil $g 'composite' $jpg 'jpg'

# Extra rows: rivals at the settings that match this package's output.
$g = 'x-png-copy'
Sharp $g 'pngadaptive' $png 'png' 'sharp adaptiveFiltering'
Vips $g 'vips copy filter=all' 'png' @('copy', "$png[access=sequential]", '%OUT%') '[filter=all]'
Ours $g 'goimgtools level 6' 'png' @('-png-level', '6') $png
Ours $g 'goimgtools level 4' 'png' @('-png-level', '4') $png
$g = 'x-webp-lossy'
Row $g 'cwebp -m 6 -af -sharp_yuv' "$O\cwebp_best.webp" $cwebp @('-quiet', '-q', '75', '-m', '6', '-af', '-sharp_yuv', $jpg, '-o', "$O\cwebp_best.webp")
Sharp $g 'webpbest' $jpg 'webp' 'sharp effort 6, smartSubsample'
Ours $g 'goimgtools effort 3' 'webp' @('-quality', '75', '-webp-effort', '3') $jpg

# Animation: a GIF of 18 photographs, 768x512.
$g = 'anim-resize'
Ours $g 'goimgtools' 'gif' @('-animated', '-resize', '384x') $anim
Row $g 'gifsicle --resize-width' "$O\gifsicle_anim.gif" $gifsicle @('--resize-width', '384', $anim, '-o', "$O\gifsicle_anim.gif")
Row $g 'sharp' "$O\sharp_animresize.gif" 'node' @("$H\bench_sharp.js", 'animthumb384', $anim, "$O\sharp_animresize.gif", $T)
$g = 'anim-webp'
Ours $g 'goimgtools' 'webp' @('-animated') $anim
Row $g 'sharp' "$O\sharp_anim.webp" 'node' @("$H\bench_sharp.js", 'animcopy', $anim, "$O\sharp_anim.webp", $T)
Row $g 'vips copy' "$O\vips_anim.webp" $vips @('copy', "$anim[n=-1]", "$O\vips_anim.webp")
Row $g 'Pillow' "$O\pil_anim.webp" 'python' @('-c', "from PIL import Image; im=Image.open(r'$anim'); im.save(r'$O\pil_anim.webp', save_all=True)")

# More rows at matched settings.
$g = 'x-anim'
Row $g 'gifsicle lanczos3 -O3' "$O\gifsicle_anim2.gif" $gifsicle @('-O3', '--resize-method', 'lanczos3', '--resize-width', '384', $anim, '-o', "$O\gifsicle_anim2.gif")
Ours $g 'goimgtools webp effort 1' 'webp' @('-animated', '-webp-effort', '1') $anim
Ours $g 'goimgtools gif kernel box' 'gif' @('-animated', '-resize', '384x', '-kernel', 'box') $anim
$g = 'x-blur'
Ours $g 'goimgtools blur libvips method' 'jpg' @('-blur', '2', '-blur-method', 'libvips', '-quality', '85') $jpg
$g = 'x-rot90'
Ours $g 'goimgtools rot90 mem 256' 'jpg' @('-mem', '256', '-rotate', '90', '-quality', '85') $jpg
