// node more_sharp.js task in out [threads] : one task of bench_more.ps1 with
// sharp, as this package does it there. JPEG out at quality 85 unless the
// task says otherwise.
const fs = require('fs');
const path = require('path');
const sharp = require(process.env.SHARP_DIR);
const [task, input, output, threads] = process.argv.slice(2);
sharp.concurrency(parseInt(threads || '1'));
sharp.cache(false);

const jpeg = s => s.jpeg({ quality: 85 });
const tasks = {
  thumb: s => jpeg(s.resize({ width: 1536 })),
  // Writing, same size.
  tiff_none: s => s.tiff({ compression: 'none' }),
  tiff_deflate: s => s.tiff({ compression: 'deflate', predictor: 'horizontal' }),
  tiff_lzw: s => s.tiff({ compression: 'lzw', predictor: 'horizontal' }),
  tiff_jpeg: s => s.tiff({ compression: 'jpeg', quality: 85 }),
  tiff_tile: s => s.tiff({ compression: 'deflate', tile: true, tileWidth: 256, tileHeight: 256 }),
  tiff_pyramid: s => s.tiff({ compression: 'deflate', tile: true, tileWidth: 256, tileHeight: 256, pyramid: true }),
  png1: s => s.png({ compressionLevel: 1 }),
  png4: s => s.png({ compressionLevel: 4 }),
  png9: s => s.png({ compressionLevel: 9 }),
  png9f: s => s.png({ compressionLevel: 9, adaptiveFiltering: true }),
  png256: s => s.png({ palette: true, colours: 256 }),
  gif: s => s.gif(),
  gif1536: s => s.resize({ width: 1536 }).gif(),
  webp_nl60: s => s.webp({ nearLossless: true, quality: 60 }),
  webp_ll6: s => s.webp({ lossless: true, effort: 6 }),
  jpg_prog: s => s.jpeg({ quality: 85, progressive: true }),
  jpg_opt: s => s.jpeg({ quality: 85, optimiseCoding: true }),
  jpg_trellis: s => s.jpeg({ quality: 85, trellisQuantisation: true }),
  jpg_moz: s => s.jpeg({ quality: 85, mozjpeg: true }),
  jpg_444: s => s.jpeg({ quality: 85, chromaSubsampling: '4:4:4' }),
  jpg_dering: s => s.jpeg({ quality: 85, overshootDeringing: true }),
  dz: s => s.tile({ size: 256 }),
  dz_google: s => s.tile({ size: 256, layout: 'google' }),
  dz_zoomify: s => s.tile({ size: 256, layout: 'zoomify' }),
  // Options.
  crop: s => jpeg(s.extract({ left: 1500, top: 1000, width: 3000, height: 2000 })),
  trim: s => jpeg(s.trim({ threshold: 10 })),
  extend_mirror: s => jpeg(s.extend({ top: 100, right: 100, bottom: 100, left: 100, extendWith: 'mirror' })),
  flip: s => jpeg(s.flip()),
  flop: s => jpeg(s.flop()),
  rot180: s => jpeg(s.rotate(180)),
  nearest: s => jpeg(s.resize({ width: 1536, kernel: 'nearest' })),
  attention: s => jpeg(s.resize(1536, 1536, { fit: 'cover', position: 'attention' })),
  entropy: s => jpeg(s.resize(1536, 1536, { fit: 'cover', position: 'entropy' })),
  contain: s => jpeg(s.resize(1536, 1536, { fit: 'contain', background: '#ffffff' })),
  enlarge: s => jpeg(s.resize({ width: 12000 })),
  grey: s => jpeg(s.greyscale()),
  sharpen: s => jpeg(s.sharpen({ sigma: 2 })),
  blur10: s => jpeg(s.blur(10)),
  blur3: s => jpeg(s.blur(3)),
  srgb: s => jpeg(s.toColourspace('srgb')),
  keepmeta: s => jpeg(s.keepMetadata()),
  // Operations.
  affine: s => jpeg(s.affine([[0.966, -0.259], [0.259, 0.966]], { background: '#ffffff' })),
  bandbool: s => jpeg(s.bandbool('and')),
  clahe: s => jpeg(s.clahe({ width: 64, height: 64 })),
  convolve: s => jpeg(s.convolve({ width: 3, height: 3, kernel: [-1, -1, -1, -1, 9, -1, -1, -1, -1] })),
  dilate: s => jpeg(s.dilate(2)),
  erode: s => jpeg(s.erode(2)),
  extract_green: s => jpeg(s.extractChannel('green')),
  gamma: s => jpeg(s.gamma(2.2)),
  linear: s => jpeg(s.linear(1.2, -20)),
  median3: s => jpeg(s.median(3)),
  median7: s => jpeg(s.median(7)),
  negate: s => jpeg(s.negate()),
  normalise: s => jpeg(s.normalise()),
  recomb: s => jpeg(s.recomb([[0.393, 0.769, 0.189], [0.349, 0.686, 0.168], [0.272, 0.534, 0.131]])),
  threshold: s => jpeg(s.threshold(128)),
  tint: s => jpeg(s.tint('#704214')),
  ensure_alpha: s => s.ensureAlpha(0.5).tiff({ compression: 'none' }),
  unflatten: s => s.unflatten().tiff({ compression: 'none' }),
  remove_alpha: s => s.removeAlpha().tiff({ compression: 'none' }),
  negate_alpha: s => s.ensureAlpha(0.5).negate({ alpha: true }).tiff({ compression: 'none' }),
  // Watermarks and text.
  logo_tiled: s => jpeg(s.composite([{ input: process.env.LOGO, tile: true }])),
  text_mark: s => jpeg(s.composite([{ input: { text: { text: '<span foreground="white">Copyright 2026</span>', font: 'Arial', dpi: 300, rgba: true } }, gravity: 'southeast' }])),
};

async function main() {
  if (task === 'text_image') {
    const words = 'The quick brown fox jumps over the lazy dog. '.repeat(40);
    await sharp({ text: { text: words, font: 'Arial', width: 2000, dpi: 220 } }).png().toFile(output);
    return;
  }
  if (task === 'stats') {
    await sharp(input, { limitInputPixels: false }).stats();
    return;
  }
  if (task === 'info') {
    await sharp(input, { limitInputPixels: false }).metadata();
    return;
  }
  if (task === 'batch') {
    // Every photograph of a directory to 1536 wide, three at a time.
    fs.mkdirSync(output, { recursive: true });
    const files = fs.readdirSync(input).filter(f => /\.jpe?g$/i.test(f));
    let next = 0;
    const worker = async () => {
      while (next < files.length) {
        const f = files[next++];
        await sharp(path.join(input, f), { limitInputPixels: false }).resize({ width: 1536 }).jpeg({ quality: 85 }).toFile(path.join(output, f));
      }
    };
    await Promise.all([worker(), worker(), worker()]);
    return;
  }
  if (task === 'anim_gif') {
    await sharp(input, { animated: true, limitInputPixels: false }).gif().toFile(output);
    return;
  }
  const fn = tasks[task];
  if (!fn) throw new Error('task ' + task);
  await fn(sharp(input, { limitInputPixels: false })).toFile(output);
}
main().catch(e => { console.error(e); process.exit(1); });
