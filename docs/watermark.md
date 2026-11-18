English | [中文](zh-CN/watermark.md)

# Invisible watermarks

`InvisibleWatermark` hides a few bytes (1 to 32: a user ID, an order number) in a picture, and `inspect.ReadWatermark` reads them back from a copy, with the same key and without the original. Two kinds:

- **`WatermarkBlind`** (the default) marks where a picture comes from. It survives what happens to a file: saving again as JPEG or WebP, resizing, cropping, turning, brightness and contrast, screenshots. At its default strength it is hard to see (see the measurements).
- **`WatermarkScreen`** is a mark against leaks. It also survives a photograph of a screen showing the picture: perspective, the lens' blur, moiré, the camera's processing. So a picture shown to someone and photographed or screenshotted can be traced to them. It is stronger and has larger details, which leaves a faint texture that can be seen up close in flat areas.

Both kinds take a `Strength` and a `Density`.

```go
// Mark: the key is the secret that reads (and forges) the mark.
err := goimgtools.Open("in.jpg").
    InvisibleWatermark([]byte("user-1234"), goimgtools.InvisibleWatermarkOptions{
        Kind: goimgtools.WatermarkScreen, Key: "our secret",
    }).
    JPEG(goimgtools.JPEGOptions{Quality: 90}).
    ToFile("shown.jpg")

// Read: from a copy, a screenshot, a photograph cropped to the screen.
mark, found, err := inspect.ReadWatermark(goimgtools.Open("leaked.jpg"), "our secret")
```

Reading a picture of 3000×2000 takes about 0.8 seconds when the mark is there and 1.6 when it is not (more ways are tried), and about 80MB. With `Threads` (or `SetDefaultThreads`) the search shares its work out and finds the same: on 8 cores, 0.6 and 0.8 seconds.

```bash
goimgtools -mark user-1234 -mark-key "our secret" -mark-kind screen in.jpg shown.jpg
goimgtools readmark -key "our secret" leaked.jpg
```

## Settings

| Field | Meaning |
|---|---|
| `Kind` | `WatermarkBlind` or `WatermarkScreen` (see above) |
| `Key` | The secret the mark is made with. Only the same key finds and reads the mark, and without it a mark cannot be forged. The empty key is a key like any other |
| `Strength` | Scales the mark's amplitude: 1 (the default) is the kind's balance; 0.5 is harder to see and to find; 2 survives more and is easier to see; at most 16 |
| `Density` | About how many times the mark repeats along the shorter side, 1 to 16 (default 4). Denser, a smaller piece of the picture still carries a whole mark: a crop must keep about two repeats each way. The mark's details are then finer, so a picture reduced much more is harder to read. Each detail is at least 2 pixels, 3 for `WatermarkScreen` |

Fewer bytes are read back from more damaged copies: the bytes are protected by a convolutional code (constraint length 9, rate 1/6) spread over thousands of places, and fewer bytes leave more of them for each bit. What is read is practically never a wrong mark: a 32-bit CRC of the key and the bytes is checked for each of the two thousand or so readings a search tries, which leaves about one chance in two million that a picture without a mark, or read with another key, gives one; otherwise `found == false`.

## How it works

- **The tile.** The mark is a tile of 64 × 64 chips repeated over the picture's brightness, each chip a few pixels wide. Each chip's value mixes three signals drawn from the key: a reference of 16 sinusoids at known frequencies (25% of its power), a random pattern of signs (sync, 20%), and the payload (55%). The reference is what the eye sees most of the mark, the more so the lower its frequencies: it is kept to a quarter of the power, between 10 and 20 cycles across a tile, which lets the whole be stronger for the same visibility. The payload's bits are coded and spread over the chips.
- **Where it goes.** The tile is added where the picture hides it best: more in busy areas, less in flat ones (the noise visibility function of Voloshynovskiy et al.). It is pushed back from pure white and black, which it could not otherwise go past.
- **Rows as they come.** The rows are marked as they arrive, from a window of a few rows around each. The result is the same on every processor and with any number of threads.
- **Finding the tiling.** To read a copy, its spectrum is flattened, and each place is weighted by how clean it is, so that flat areas (where the flattened picture is mostly the mark) count as much as busy ones. The reference's peaks are then searched for over sizes and angles, mirrored or not, in the whole picture and in its middle. This is the template method of Pereira and Pun and of Digimarc; in a photograph the middle is less distorted by perspective.
- **Placing it.** Every copy of the tile is folded onto one, which adds up the mark and averages the picture away. The sync pattern, with some of the reference, says where the tiles start. The placement is corrected from halves of the picture, then region by region with a homography, or, where the regions are too weak, from the tile's size in four windows (a homography changes the area a tile covers as the cube of its denominator). Finally the payload is decoded. For it the tiles are folded again with each place weighed: the places are put in twelve classes by how busy the flattened picture is around them, and each class counts by how much of the mark it holds over its noise, as measured on what every tile has (the sync pattern and the reference). Where the picture is flat, compression leaves little of the mark; where it is busy, the picture drowns it; the places between hold the most. The same known part then shows how much the copy weakened the mark at each frequency, as resampling and compression blur it: that much of it is taken out, and the rest is weighed by the same response over the noise left at that frequency (a matched filter). The likeliest bit sequences of every length of payload are checked against the CRC, more of them for the lengths that fit best (Viterbi's algorithm, then a best-first search back through its trellis that yields the sequences in order of likelihood).

## Measurements

The test pictures are three photographs (1500×844, 1280×720, 1600×500), a small photograph (640×360) and a screenshot of a user interface, mostly white (1280×900). Each carries a 9-byte mark. The same attacks, written with Pillow, were made on each marked picture: [blind_watermark](https://github.com/guofei9987/blind_watermark), the most used open-source library of the kind, was measured the same way at its default settings. A cell is the number of pictures, of 5, whose mark was read back right.

| Attack | Blind (default) | Blind, `Strength: 1.5` | Screen (default) | blind_watermark |
|---|---|---|---|---|
| None | 5 | 5 | 5 | 5 |
| JPEG quality 90 / 70 / 50 / 30 | 5 / 5 / 4 / 4 | 5 / 5 / 4 / 4 | 5 / 5 / 5 / 4 | 5 / 4 / 4 / 3 |
| WebP quality 60 | 3 | 3 | 4 | 3 |
| Half the size | 5 | 5 | 5 | 0 |
| 70% of the size, then JPEG 80 | 5 | 5 | 5 | 0 |
| Crop to 40% / 25% of each side | 4 / 1 | 4 / 2 | 3 / 2 | 0 / 0 |
| Turned by 3° / 30°, cropped | 5 / 4 | 5 / 5 | 5 / 5 | 0 / 0 |
| Darker (×0.8) / more contrast (×1.3) | 5 / 4 | 5 / 4 | 5 / 5 | 4 / 4 |
| Noise (σ 8) | 4 | 5 | 5 | 5 |
| Screenshot (shown at 1366 wide, JPEG 90) | 5 | 5 | 5 | 3 |
| Perspective (corners moved up to 4%), JPEG 85 | 5 | 5 | 5 | 0 |
| Photograph of a screen (simulated: perspective, lens blur, 3/4 the size, gamma, moiré, sensor noise, sharpening, JPEG 85) | 2 | 4 | 5 | 0 |

How much each changes the picture (means over the five; SSIMULACRA2: 90 and above is indistinguishable, 70 high quality; butteraugli: under 1 invisible):

| | PSNR | SSIMULACRA2 | butteraugli |
|---|---|---|---|
| Blind (default) | 45.3 dB | 79.7 | 3.9 |
| Blind, `Strength: 1.5` | 41.8 dB | 68.1 | 5.8 |
| Screen (default) | 38.9 dB | 51.0 | 9.4 |
| blind_watermark (default) | 37.7 dB | 40.4 | 5.5 |

blind_watermark cannot read its mark from a picture whose size or angle has changed. Its `recover` needs the original to put the picture back first. So it fails every geometric attack here. At `Strength: 1.5`, which changes the picture less than blind_watermark by PSNR and SSIMULACRA2 and more by butteraugli, the blind mark here survives JPEG better than it does, WebP and strong noise as well, and every geometric attack too.

Against the other libraries of the kind, on the same pictures and attacks (each at its defaults but where said; JPEG 90 / 70 / 50 / 30):

| | PSNR | SSIMULACRA2 | butteraugli | JPEG | WebP 60 | Crop to 40% | Turned 3° | Perspective | Photograph of a screen |
|---|---|---|---|---|---|---|---|---|---|
| Blind (this package) | 45.3 dB | 79.7 | 3.9 | 5 / 5 / 4 / 4 | 3 | 4 | 5 | 5 | 2 |
| Blind, `Strength: 1.5` | 41.8 dB | 68.1 | 5.8 | 5 / 5 / 4 / 4 | 3 | 4 | 5 | 5 | 4 |
| Screen (this package) | 38.9 dB | 51.0 | 9.4 | 5 / 5 / 5 / 4 | 4 | 3 | 5 | 5 | 5 |
| [blind_watermark](https://github.com/guofei9987/blind_watermark) | 37.7 dB | 40.4 | 5.5 | 5 / 4 / 4 / 3 | 3 | 0 | 0 | 0 | 0 |
| [invisible-watermark](https://github.com/ShieldMnt/invisible-watermark) `dwtDctSvd` (as in Stable Diffusion) | 40.5 dB | 75.1 | 3.4 | 4 / 0 / 0 / 0 | 0 | 0 | 0 | 0 | 0 |
| [TrustMark](https://github.com/adobe/trustmark) `Q` (Adobe's default) | 45.1 dB | 68.8 | 8.3 | 5 / 5 / 5 / 4 | 5 | 0 | 0 | 4 | 4 |
| TrustMark `P` | 50.0 dB | 86.3 | 3.7 | 5 / 4 / 3 / 0 | 4 | 0 | 0 | 0 | 0 |

invisible-watermark reads its mark back from 4 of the 5 untouched pictures (`dwtDct`: 3, less visible: SSIMULACRA2 87.1). TrustMark, a pair of neural networks that need PyTorch, survives recompression best: at about the visibility of the blind mark at `Strength: 1.5` (its model `Q`), it reads 5 / 5 / 5 / 4 at JPEG 90 to 30, against 5 / 5 / 4 / 4 here (the blind mark reads 4 of 5 at JPEG 30 at its default too), and 5 at WebP 60, against 3 here. But it reads nothing that was cropped or turned, as its decoder takes the whole picture as it is. The marks here are the only ones of these that survive crops and turns.

On a standard set, the 24 photographs of the [Kodak suite](https://r0k.us/graphics/kodak/) (768 × 512), with the same attacks; a cell is the number read back, of 24:

| | PSNR | SSIMULACRA2 | butteraugli | JPEG 90 / 70 / 50 / 30 | WebP 60 | Crop to 40% / 25% | Turned 3° / 30° | Perspective | Photograph of a screen |
|---|---|---|---|---|---|---|---|---|---|
| Blind (this package) | 42.5 dB | 76.9 | 2.7 | 24 / 24 / 24 / 23 | 23 | 14 / 3 | 23 / 23 | 24 | 21 |
| Blind, `Strength: 1.5` | 39.0 dB | 63.2 | 4.1 | 24 / 24 / 24 / 24 | 24 | 20 / 10 | 24 / 24 | 24 | 24 |
| Screen (this package) | 35.6 dB | 30.8 | 8.3 | 24 / 24 / 24 / 24 | 24 | 12 / 0 | 23 / 23 | 24 | 24 |
| blind_watermark | 38.0 dB | 43.4 | 4.6 | 24 / 24 / 22 / 0 | 19 | 0 / 0 | 0 / 0 | 0 | 0 |
| invisible-watermark `dwtDctSvd` | 39.4 dB | 75.2 | 1.4 | 24 / 0 / 0 / 0 | 0 | 0 / 0 | 0 / 0 | 0 | 0 |
| TrustMark `Q` | 42.5 dB | 54.7 | 9.5 | 24 / 24 / 24 / 22 | 24 | 0 / 0 | 0 / 0 | 21 | 11 |
| TrustMark `P` | 49.4 dB | 87.4 | 2.6 | 24 / 19 / 1 / 0 | 11 | 0 / 0 | 0 / 0 | 0 | 0 |

The other attacks (half the size, 70% and JPEG 80, darker, more contrast, noise, a screenshot) read 23 or 24 of 24 for the marks here and for TrustMark (`P`: 22 after more contrast); blind_watermark and invisible-watermark read none after halving. On these photographs the blind mark at its default changes the picture much less than TrustMark `Q` (SSIMULACRA2 76.9 against 54.7, butteraugli 2.7 against 9.5) and reads about as many after JPEG and WebP (23 and 23 at JPEG 30 and WebP 60, against 22 and 24); TrustMark `P`, about as visible by butteraugli, reads one at JPEG 50. Busy photographs hide a mark well: the first set's mostly white screenshot and its long, low photograph are harder.

Everyday processing, done with this package's own operations, left every blind mark at its default readable, on the five pictures above with three keys (15 marks) and on the 24 Kodak photographs: 1.5 times larger, grey, saturation halved, hue turned by 60°, JPEG 80 three times over, 256 colours, sharpening, gamma 0.8, a 3 × 3 median, a blur of 1, normalising, turned by 90°, mirrored, twice larger then JPEG 80, the longer side to 1080 then JPEG 85. Harder ones read fewer (of 15 and of 24): a blur of 2, 12 and 20; a 5 × 5 median, 15 and 21; JPEG 50 three times over, 12 and 24; the middle half enlarged to the whole, 12 and 15. Reduced to a third, the Kodak photographs (to 256 × 170) keep none: the mark's details become smaller than a pixel; of the larger pictures above, 9 of 15 are read.

A harder simulation of a phone's photograph, done with this package's own operations, has stronger perspective and Pillow's sharpening. There the screen mark is read from all three photographs and not from the small photograph or the screenshot of a user interface; leaving any one of its steps out, from 3 to 5 of the 5.

## Limits

- **Visibility and robustness trade off.** A mark that survives a camera is a faint texture up close in flat areas. One that cannot be seen at all survives less. `Strength` moves along that line: the blind mark at 1 suits pictures that stay digital: here it was read from the photographs saved again as JPEG down to quality 30, and as WebP at 60 from three of the five pictures (WebP's filters take more of it); use 1.5 to 2 for harder recompression and for flat pictures; the screen mark is for what may be photographed.
- **Flat pictures carry less.** A user interface or a document, mostly white, has few places to hide the mark. Its flat areas carry it weakly, so raise `Strength` for such pictures.
- **Small pictures and small crops carry fewer repeats.** A picture of 640×360 or less is read more poorly after heavy damage. A crop must keep about two repeats each way: raise `Density` when crops are likely, which also makes the mark's details finer.
- **Photographs of a screen.** Crop the photograph to the screen, and keep the angle moderate. The simulations above are what is measured; real phones, screens and distances vary.
- **It is not a lock.** Someone who knows the mark is there can still damage it beyond reading (heavy noise, a strong blur, re-drawing the picture). The key keeps others from reading the mark or making a false one, not from destroying it.
