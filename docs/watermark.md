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

Fewer bytes are read back from more damaged copies: the bytes are protected by a convolutional code spread over thousands of places, and fewer bytes leave more of them for each bit. What is read is practically never a wrong mark: a 32-bit CRC of the key and the bytes is checked for each of the thousand or so readings a search tries, which leaves about one chance in a million that a picture without a mark, or read with another key, gives one; otherwise `found == false`.

## How it works

- **The tile.** The mark is a tile of 64 × 64 chips repeated over the picture's brightness, each chip a few pixels wide. Each chip's value mixes three signals drawn from the key: a reference of 24 sinusoids at known frequencies, a random pattern of signs (sync), and the payload. The payload's bits are coded and spread over the chips.
- **Where it goes.** The tile is added where the picture hides it best: more in busy areas, less in flat ones (the noise visibility function of Voloshynovskiy et al.). It is pushed back from pure white and black, which it could not otherwise go past.
- **Rows as they come.** The rows are marked as they arrive, from a window of a few rows around each. The result is the same on every processor and with any number of threads.
- **Finding the tiling.** To read a copy, its spectrum is flattened, and each place is weighted by how clean it is, so that flat areas (where the flattened picture is mostly the mark) count as much as busy ones. The reference's peaks are then searched for over sizes and angles, mirrored or not, in the whole picture and in its middle. This is the template method of Pereira and Pun and of Digimarc; in a photograph the middle is less distorted by perspective.
- **Placing it.** Every copy of the tile is folded onto one, which adds up the mark and averages the picture away. The sync pattern, with some of the reference, says where the tiles start. The placement is corrected from halves of the picture, then region by region with a homography, or, where the regions are too weak, from the tile's size in four windows (a homography changes the area a tile covers as the cube of its denominator). Finally the payload is decoded. What every tile has (the sync pattern and the reference) shows how much the copy weakened the mark at each frequency, as resampling and compression blur it: that much of it is taken out, and the rest is weighed by the same response over the noise left at that frequency (a matched filter). The four likeliest bit sequences (a list Viterbi decoder) are checked in turn.

## Measurements

The test pictures are three photographs (1500×844, 1280×720, 1600×500), a small photograph (640×360) and a screenshot of a user interface, mostly white (1280×900). Each carries a 9-byte mark. The same attacks, written with Pillow, were made on each marked picture: [blind_watermark](https://github.com/guofei9987/blind_watermark), the most used open-source library of the kind, was measured the same way at its default settings. A cell is the number of pictures, of 5, whose mark was read back right.

| Attack | Blind (default) | Blind, `Strength: 1.5` | Screen (default) | blind_watermark |
|---|---|---|---|---|
| None | 5 | 5 | 5 | 5 |
| JPEG quality 90 / 70 / 50 / 30 | 5 / 4 / 4 / 2 | 5 / 4 / 4 / 3 | 5 / 5 / 5 / 4 | 5 / 4 / 4 / 3 |
| WebP quality 60 | 1 | 2 | 4 | 3 |
| Half the size | 4 | 5 | 5 | 0 |
| 70% of the size, then JPEG 80 | 3 | 4 | 5 | 0 |
| Crop to 40% / 25% of each side | 2 / 1 | 4 / 2 | 3 / 1 | 0 / 0 |
| Turned by 3° / 30°, cropped | 4 / 4 | 5 / 4 | 5 / 4 | 0 / 0 |
| Darker (×0.8) / more contrast (×1.3) | 5 / 4 | 5 / 4 | 5 / 4 | 4 / 4 |
| Noise (σ 8) | 4 | 4 | 5 | 5 |
| Screenshot (shown at 1366 wide, JPEG 90) | 5 | 5 | 5 | 3 |
| Perspective (corners moved up to 4%), JPEG 85 | 5 | 5 | 5 | 0 |
| Photograph of a screen (simulated: perspective, lens blur, 3/4 the size, gamma, moiré, sensor noise, sharpening, JPEG 85) | 3 | 5 | 5 | 0 |

How much each changes the picture (means over the five; SSIMULACRA2: 90 and above is indistinguishable, 70 high quality; butteraugli: under 1 invisible):

| | PSNR | SSIMULACRA2 | butteraugli |
|---|---|---|---|
| Blind (default) | 45.8 dB | 79.5 | 4.6 |
| Blind, `Strength: 1.5` | 42.3 dB | 68.0 | 7.0 |
| Screen (default) | 39.4 dB | 50.9 | 10.2 |
| blind_watermark (default) | 37.7 dB | 40.4 | 5.5 |

blind_watermark cannot read its mark from a picture whose size or angle has changed. Its `recover` needs the original to put the picture back first. So it fails every geometric attack here. At `Strength: 1.5`, which changes the picture less than blind_watermark by PSNR and SSIMULACRA2 and more by butteraugli, the blind mark here survives JPEG as well as it does (WebP and strong noise a little less), and every geometric attack too.

A harder simulation of a phone's photograph, done with this package's own operations, has stronger perspective and Pillow's sharpening. There the screen mark is read from two of the three photographs (the third is at the edge: its copy decoded by Go's own JPEG decoder is read) and not from the small photograph or the screenshot of a user interface; leaving any one of its steps out, from 3 or 4 of the 5.

## Limits

- **Visibility and robustness trade off.** A mark that survives a camera is a faint texture up close in flat areas. One that cannot be seen at all survives less. `Strength` moves along that line: the blind mark at 1 suits pictures that stay digital and are saved again as JPEG at quality 70 or more, or as WebP at about 90 (WebP's filters take more of it); use 1.5 to 2 for harder recompression; the screen mark is for what may be photographed.
- **Flat pictures carry less.** A user interface or a document, mostly white, has few places to hide the mark. Its flat areas carry it weakly, so raise `Strength` for such pictures.
- **Small pictures and small crops carry fewer repeats.** A picture of 640×360 or less is read more poorly after heavy damage. A crop must keep about two repeats each way: raise `Density` when crops are likely, which also makes the mark's details finer.
- **Photographs of a screen.** Crop the photograph to the screen, and keep the angle moderate. The simulations above are what is measured; real phones, screens and distances vary.
- **It is not a lock.** Someone who knows the mark is there can still damage it beyond reading (heavy noise, a strong blur, re-drawing the picture). The key keeps others from reading the mark or making a false one, not from destroying it.
