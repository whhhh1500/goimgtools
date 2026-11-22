const sharp = require(process.env.SHARP_DIR);
const [task, input, output, w, h] = process.argv.slice(2);
sharp.cache(false);
sharp(input, { limitInputPixels: false }).resize({ width: +w, height: +h, fit: 'fill', kernel: 'lanczos3' }).png().toFile(output).then(() => {}, e => { console.error(e); process.exit(1); });
