// node bench_sharp.js task in out [threads] : one task with sharp, as the
// other tools do it. Tasks: thumb, copy, blur, sharpen, rot90, rot30, gray,
// modulate, composite (needs LOGO), lossless (copy to lossless WebP).
const sharp = require(process.env.SHARP_DIR);
const [task, input, output, threads] = process.argv.slice(2);
sharp.concurrency(parseInt(threads || '1'));
sharp.cache(false);
let s = sharp(input, { limitInputPixels: false, animated: task.startsWith('anim') });
switch (task) {
  case 'thumb': s = s.resize({ width: 1536 }); break;
  case 'thumbslow': s = s.resize({ width: 1536, fastShrinkOnLoad: false }); break;
  case 'copy': case 'lossless': case 'animcopy': case 'pngadaptive': case 'webpbest': break;
  case 'animthumb384': s = s.resize({ width: 384 }); break;
  case 'blur': s = s.blur(2); break;
  case 'sharpen': s = s.sharpen({ sigma: 2 }); break;
  case 'rot90': s = s.rotate(90); break;
  case 'rot30': s = s.rotate(30, { background: '#ffffff' }); break;
  case 'gray': s = s.greyscale(); break;
  case 'modulate': s = s.modulate({ brightness: 1.1, saturation: 1.3, hue: 20 }); break;
  case 'composite': s = s.composite([{ input: process.env.LOGO, gravity: 'southeast' }]); break;
  default: throw new Error('task ' + task);
}
const ext = output.split('.').pop().toLowerCase();
if (ext === 'jpg') s = s.jpeg({ quality: 85 });
else if (ext === 'webp') s = s.webp(task === 'lossless' ? { lossless: true } : task === 'webpbest' ? { quality: 75, effort: 6, smartSubsample: true } : { quality: 75 });
else if (ext === 'png') s = s.png(task === 'pngadaptive' ? { adaptiveFiltering: true } : {});
else if (ext === 'gif') s = s.gif();
else if (ext === 'tif') s = s.tiff({ compression: 'none' });
s.toFile(output).then(() => {}, e => { console.error(e); process.exit(1); });
