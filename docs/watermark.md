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

Reading a picture of 3000×2000 takes about 0.8 seconds when the mark is there and 5 when it is not (more ways are tried, in more places, and a picture in colour is read twice, see below), and about 90MB (105 when nothing is found). With `Threads` (or `SetDefaultThreads`) the search shares its work out and finds the same: on 8 cores, 0.5 and 2 seconds.

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
| `Density` | About how many times the mark repeats along the shorter side, 1 to 16 (default 4). Denser, a smaller piece of the picture still carries a whole mark: a crop must keep about two repeats each way. The mark's details are then finer, so a picture reduced much more is harder to read. Each detail is at least 2 pixels, 3 for `WatermarkScreen`; on a busy picture under 300 pixels on its shorter side the blind mark goes down to details of 1 pixel, to keep enough repeats |

Fewer bytes are read back from more damaged copies: the bytes are protected by a convolutional code (constraint length 9, rate 1/6) spread over thousands of places, and fewer bytes leave more of them for each bit. What is read is practically never a wrong mark: a 32-bit CRC of the key and the bytes is checked for each of the six thousand or so readings a search tries at most, which leaves about one chance in seven hundred thousand that a picture without a mark, or read with another key, gives one; otherwise `found == false`.

## How it works

- **The tile.** The mark is a tile of 64 × 64 chips repeated over the picture's brightness, each chip a few pixels wide. Each chip's value mixes three signals drawn from the key: a reference of 16 sinusoids at known frequencies (25% of its power), a random pattern of signs (sync, 20%), and the payload (55%). The reference is what the eye sees most of the mark, the more so the lower its frequencies: it is kept to a quarter of the power, between 12 and 21 cycles across a tile, which lets the whole be stronger for the same visibility. The payload's bits are coded and spread over the chips. The sync pattern and the payload, drawn chip by chip, are then tilted towards the higher frequencies (weighed by the square root of the frequency), which the eye sees less: they can be stronger for the same visibility, and enough of them outlasts compression for the reader, which weighs them alike. Last, each chip is divided by the strength of the chips around it, and again by that of the tile's coarser structure around it: where the three signals happen to add up, the same in every copy of the tile, the mark would show first.
- **Where it goes.** The tile is added where the picture hides it best: more in busy areas, less in flat ones (the noise visibility function of Voloshynovskiy et al.). It is pushed back from pure white and black, which it could not otherwise go past.
- **Rows as they come.** The rows are marked as they arrive, from a window of a few rows around each. The result is the same on every processor and with any number of threads.
- **Finding the tiling.** To read a copy, its spectrum is flattened (each frequency over the mean of the 15 × 15 around it), and each place is evened out over the few pixels around it, so that flat areas (where the flattened picture is mostly the mark) count as much as busy ones, even where compression left little of the mark there. The reference's peaks are then searched for over sizes and angles, mirrored or not, in the whole picture and in its middle, and, when nothing is read from those, in its four corners, where a mark left in only part of the picture stands out more. This is the template method of Pereira and Pun and of Digimarc; in a photograph the middle is less distorted by perspective.
- **Placing it.** Every copy of the tile is folded onto one, which adds up the mark and averages the picture away. The sync pattern, with some of the reference, says where the tiles start. The placement is corrected from halves of the picture, then region by region with a homography, or, where the regions are too weak, from the tile's size in four windows (a homography changes the area a tile covers as the cube of its denominator). Finally the payload is decoded. For it the tiles are folded again with each place weighed: the places are put in twelve classes by how busy the flattened picture is around them, and each class counts by how much of the mark it holds over its noise, as measured on what every tile has (the sync pattern and the reference). Where the picture is flat, compression leaves little of the mark; where it is busy, the picture drowns it; the places between hold the most. The same known part then shows how much the copy weakened the mark at each frequency, as resampling and compression blur it: that much of it is taken out, and the rest is weighed by the same response over the noise left at that frequency (a matched filter). The likeliest bit sequences of every length of payload are checked against the CRC, more of them for the lengths that fit best (Viterbi's algorithm, then a best-first search back through its trellis that yields the sequences in order of likelihood).
- **Reading again by colour.** A mark changes red, green and blue alike, so it is all in the brightness and none in the colour; much of a photograph's texture is in both. When nothing is read from a picture in colour, its brightness is read again less what its colour explains around each place (a least-squares fit, over 9 × 9 pixels, of the brightness's fine detail on the colour's), which leaves the mark with less of the picture over it: more is read after crops and turns. The brightness as it is is read first because a camera's white balance puts some of the mark in the colour too.

## Measurements

The test pictures are three photographs (1500×844, 1280×720, 1600×500), a small photograph (640×360) and a screenshot of a user interface, mostly white (1280×900). Each carries a 9-byte mark. The same attacks, written with Pillow, were made on each marked picture: [blind_watermark](https://github.com/guofei9987/blind_watermark), the most used open-source library of the kind, was measured the same way at its default settings. A cell is the number of pictures, of 5, whose mark was read back right.

| Attack | Blind (default) | Blind, `Strength: 1.5` | Screen (default) | blind_watermark |
|---|---|---|---|---|
| None | 5 | 5 | 5 | 5 |
| JPEG quality 90 / 70 / 50 / 30 | 5 / 5 / 4 / 4 | 5 / 5 / 5 / 4 | 5 / 5 / 5 / 4 | 5 / 4 / 4 / 3 |
| WebP quality 60 | 3 | 4 | 4 | 3 |
| Half the size | 5 | 5 | 5 | 0 |
| 70% of the size, then JPEG 80 | 5 | 5 | 5 | 0 |
| Crop to 40% / 25% of each side | 4 / 2 | 4 / 3 | 3 / 2 | 0 / 0 |
| Turned by 3° / 30°, cropped | 5 / 5 | 5 / 5 | 5 / 5 | 0 / 0 |
| Darker (×0.8) / more contrast (×1.3) | 5 / 5 | 5 / 5 | 5 / 5 | 4 / 4 |
| Noise (σ 8) | 5 | 5 | 5 | 5 |
| Screenshot (shown at 1366 wide, JPEG 90) | 5 | 5 | 5 | 3 |
| Perspective (corners moved up to 4%), JPEG 85 | 5 | 5 | 5 | 0 |
| Photograph of a screen (simulated: perspective, lens blur, 3/4 the size, gamma, moiré, sensor noise, sharpening, JPEG 85) | 4 | 4 | 5 | 0 |

How much each changes the picture (means over the five; SSIMULACRA2: 90 and above is indistinguishable, 70 high quality; butteraugli: under 1 invisible):

| | PSNR | SSIMULACRA2 | butteraugli |
|---|---|---|---|
| Blind (default) | 44.5 dB | 79.8 | 3.3 |
| Blind, `Strength: 1.5` | 41.0 dB | 68.6 | 4.9 |
| Screen (default) | 38.4 dB | 53.3 | 6.9 |
| blind_watermark (default) | 37.7 dB | 40.4 | 5.5 |

blind_watermark cannot read its mark from a picture whose size or angle has changed. Its `recover` needs the original to put the picture back first. So it fails every geometric attack here. At `Strength: 1.5`, which changes the picture less than blind_watermark by all three measures, the blind mark here survives JPEG and WebP better than it does, strong noise as well, and every geometric attack too.

Against the other libraries of the kind, on the same pictures and attacks (each at its defaults but where said; JPEG 90 / 70 / 50 / 30):

| | PSNR | SSIMULACRA2 | butteraugli | JPEG | WebP 60 | Crop to 40% | Turned 3° | Perspective | Photograph of a screen |
|---|---|---|---|---|---|---|---|---|---|
| Blind (this package) | 44.5 dB | 79.8 | 3.3 | 5 / 5 / 4 / 4 | 3 | 4 | 5 | 5 | 4 |
| Blind, `Strength: 1.5` | 41.0 dB | 68.6 | 4.9 | 5 / 5 / 5 / 4 | 4 | 4 | 5 | 5 | 4 |
| Screen (this package) | 38.4 dB | 53.3 | 6.9 | 5 / 5 / 5 / 4 | 4 | 3 | 5 | 5 | 5 |
| [blind_watermark](https://github.com/guofei9987/blind_watermark) | 37.7 dB | 40.4 | 5.5 | 5 / 4 / 4 / 3 | 3 | 0 | 0 | 0 | 0 |
| [invisible-watermark](https://github.com/ShieldMnt/invisible-watermark) `dwtDctSvd` (as in Stable Diffusion) | 40.5 dB | 75.1 | 3.4 | 4 / 0 / 0 / 0 | 0 | 0 | 0 | 0 | 0 |
| [TrustMark](https://github.com/adobe/trustmark) `Q` (Adobe's default) | 45.1 dB | 68.8 | 8.3 | 5 / 5 / 5 / 4 | 5 | 0 | 0 | 4 | 4 |
| TrustMark `P` | 50.0 dB | 86.3 | 3.7 | 5 / 4 / 3 / 0 | 4 | 0 | 0 | 0 | 0 |
| [HiDDeN](https://github.com/ando-khachatryan/HiDDeN) (2018) | 31.2 dB | −4.0 | 13.0 | 2 / 3 / 2 / 3 | 1 | 0 | 0 | 0 | 0 |
| [MBRS](https://github.com/jzyustc/MBRS) (2021) | 46.6 dB | 75.5 | 6.4 | 5 / 5 / 5 / 5 | 3 | 0 | 0 | 0 | 0 |
| [CIN](https://github.com/rmpku/CIN) (2022) | 45.7 dB | 67.0 | 9.3 | 5 / 5 / 4 / 3 | 4 | 0 | 0 | 0 | 0 |
| [Watermark Anything](https://github.com/facebookresearch/watermark-anything) (Meta, `wam_mit`) | 40.2 dB | 68.6 | 2.2 | 5 / 5 / 3 / 3 | 3 | 2 | 1 | 4 | 2 |
| [Video Seal](https://github.com/facebookresearch/videoseal) 1.0 (Meta, 2024) | 48.4 dB | 83.3 | 2.2 | 4 / 4 / 2 / 2 | 2 | 0 | 0 | 2 | 1 |
| [PixelSeal](https://github.com/facebookresearch/videoseal) (Meta, 2025) | 48.1 dB | 85.1 | 2.1 | 5 / 5 / 4 / 3 | 1 | 2 | 5 | 5 | 3 |
| [ChunkySeal](https://github.com/facebookresearch/videoseal) (Meta, 2025) | 46.1 dB | 75.9 | 2.1 | 5 / 5 / 4 / 2 | 2 | 0 | 5 | 0 | 0 |
| [InvisMark](https://github.com/microsoft/InvisMark) (Microsoft, 2024) | 47.4 dB | 87.8 | 2.8 | 4 / 2 / 0 / 0 | 0 | 0 | 4 | 3 | 0 |
| [VINE](https://github.com/Shilin-LU/VINE) `VINE-R` (2025) | 31.8 dB | −8.1 | 156 | 5 / 5 / 5 / 5 | 5 | 0 | 0 | 0 | 0 |
| [RivaGAN](https://github.com/DAI-Lab/RivaGAN) (2019, in invisible-watermark) | 41.1 dB | 76.6 | 4.1 | 3 / 3 / 2 / 0 | 1 | 3 | 4 | 3 | 0 |
| [StegaStamp](https://github.com/tancik/StegaStamp) (2020) | 30.7 dB | −9.1 | 29.2 | 5 / 5 / 5 / 5 | 5 | 0 | 0 | 4 | 3 |

invisible-watermark reads its mark back from 4 of the 5 untouched pictures (`dwtDct`: 3, less visible: SSIMULACRA2 87.1). TrustMark, a pair of neural networks that need PyTorch, survives recompression best: at about the visibility of the blind mark at `Strength: 1.5` (its model `Q`), it reads 5 / 5 / 5 / 4 at JPEG 90 to 30, as the blind mark does here, and 5 at WebP 60, against 4 here. But it reads nothing that was cropped or turned, as its decoder takes the whole picture as it is. Of the others, Watermark Anything (WAM), PixelSeal and RivaGAN read some crops to 40%, and they, ChunkySeal and InvisMark slight turns; only the marks here survive a turn by 30° and a crop to a quarter. (WAM carries 32 bits, which must all come back; ChunkySeal 1024, which hold the 9 bytes fourteen times over; Video Seal and PixelSeal carry 256, which here hold the 9 bytes three times over, read by adding the three up; RivaGAN carries 32, which hold the first 4 bytes; InvisMark, VINE and StegaStamp carry 100, which hold the 9 bytes with 28 bits of BCH: a mark is read when its 72 bits are all right, as the 9 bytes here.)

On a standard set, the 24 photographs of the [Kodak suite](https://r0k.us/graphics/kodak/) (768 × 512), with the same attacks; a cell is the number read back, of 24:

| | PSNR | SSIMULACRA2 | butteraugli | JPEG 90 / 70 / 50 / 30 | WebP 60 | Crop to 40% / 25% | Turned 3° / 30° | Perspective | Photograph of a screen |
|---|---|---|---|---|---|---|---|---|---|
| Blind (this package) | 41.9 dB | 78.0 | 2.4 | 24 / 24 / 24 / 24 | 24 | 21 / 13 | 24 / 24 | 24 | 22 |
| Blind, `Strength: 0.6` | 46.2 dB | 87.5 | 1.4 | 24 / 24 / 24 / 20 | 18 | 15 / 5 | 24 / 23 | 22 | 17 |
| Blind, `Strength: 0.75` | 44.3 dB | 84.1 | 1.8 | 24 / 24 / 24 / 24 | 22 | 17 / 9 | 24 / 24 | 24 | 19 |
| Blind, `Strength: 1.5` | 38.4 dB | 65.0 | 3.6 | 24 / 24 / 24 / 24 | 24 | 24 / 21 | 24 / 24 | 24 | 24 |
| Screen (this package) | 35.0 dB | 33.6 | 6.5 | 24 / 24 / 24 / 24 | 24 | 22 / 0 | 24 / 24 | 24 | 24 |
| blind_watermark | 38.0 dB | 43.4 | 4.6 | 24 / 24 / 22 / 0 | 19 | 0 / 0 | 0 / 0 | 0 | 0 |
| invisible-watermark `dwtDctSvd` | 39.4 dB | 75.2 | 1.4 | 24 / 0 / 0 / 0 | 0 | 0 / 0 | 0 / 0 | 0 | 0 |
| TrustMark `Q` | 42.5 dB | 54.7 | 9.5 | 24 / 24 / 24 / 22 | 24 | 0 / 0 | 0 / 0 | 21 | 11 |
| TrustMark `P` | 49.4 dB | 87.4 | 2.6 | 24 / 19 / 1 / 0 | 11 | 0 / 0 | 0 / 0 | 0 | 0 |
| HiDDeN | 30.4 dB | −5.7 | 9.9 | 7 / 4 / 4 / 2 | 3 | 0 / 0 | 0 / 0 | 0 | 0 |
| MBRS | 45.8 dB | 76.7 | 5.5 | 24 / 24 / 23 / 22 | 20 | 0 / 0 | 0 / 0 | 0 | 0 |
| CIN | 45.4 dB | 72.0 | 6.2 | 24 / 24 / 23 / 12 | 19 | 0 / 0 | 0 / 0 | 0 | 0 |
| WAM | 38.9 dB | 71.6 | 1.5 | 24 / 24 / 24 / 10 | 14 | 22 / 0 | 10 / 0 | 23 | 22 |
| Video Seal 1.0 | 45.3 dB | 80.8 | 1.9 | 24 / 24 / 23 / 20 | 21 | 0 / 0 | 1 / 0 | 20 | 11 |
| PixelSeal | 46.3 dB | 83.9 | 1.8 | 24 / 24 / 23 / 22 | 22 | 0 / 0 | 24 / 0 | 24 | 12 |
| ChunkySeal | 45.1 dB | 80.1 | 1.7 | 23 / 18 / 11 / 7 | 4 | 0 / 0 | 23 / 0 | 0 | 0 |
| InvisMark | 50.4 dB | 90.9 | 1.5 | 24 / 6 / 0 / 0 | 0 | 0 / 0 | 24 / 0 | 4 | 0 |
| VINE-R | 35.0 dB | 46.2 | 60.6 | 24 / 24 / 24 / 24 | 24 | 0 / 0 | 0 / 0 | 0 | 0 |
| RivaGAN | 40.5 dB | 77.7 | 2.1 | 23 / 21 / 11 / 2 | 9 | 0 / 0 | 24 / 0 | 23 | 4 |
| StegaStamp | 29.7 dB | −17.6 | 22.7 | 24 / 24 / 24 / 24 | 24 | 0 / 0 | 0 / 0 | 24 | 24 |

The other attacks (half the size, 70% and JPEG 80, darker, more contrast, noise, a screenshot) read 23 or 24 of 24 for the marks here at their defaults, for TrustMark (`P`: 22 after more contrast), MBRS, CIN, WAM, Video Seal, PixelSeal and VINE (after 70% and JPEG 80, CIN 22, ChunkySeal 13 and InvisMark none); blind_watermark and invisible-watermark read none after halving. On these photographs the blind mark at its default changes the picture much less than TrustMark `Q` (SSIMULACRA2 78.0 against 54.7, butteraugli 2.4 against 9.5) and reads as many or more after JPEG and WebP (24 and 24 at JPEG 30 and WebP 60, against 22 and 24); TrustMark `P`, about as visible by butteraugli, reads one at JPEG 50. Made as faint as TrustMark `P` by SSIMULACRA2 (`Strength: 0.6`, 87.5 against 87.4, and less visible by butteraugli, 1.4 against 2.6), the blind mark reads as many untouched and at JPEG 90 (24), more after harder JPEG (24, 24 and 20 at 70, 50 and 30, against 19, 1 and 0), after WebP 60 (18 against 11) and after every geometric attack, where TrustMark `P` reads none, and 21 to 24 after the other attacks, against 22 to 24. Busy photographs hide a mark well: the first set's mostly white screenshot and its long, low photograph are harder.

Meta's PixelSeal (December 2025), which its authors place at the front of what is known for robustness against invisibility, survives recompression almost as well as TrustMark `Q` while changing the picture far less (SSIMULACRA2 83.9 against 54.7). As visible as it by both measures (`Strength: 0.75`: SSIMULACRA2 84.1 against 83.9, butteraugli 1.78 against 1.78), the blind mark reads as many or more after every attack but 70% and JPEG 80 (23 against 24): more after JPEG 50 and 30 (24 and 24 against 23 and 22), WebP 70 and 75 (24 and 24 against 22), crops (17 and 9 against none), a turn by 30° (24 against none) and a photograph of a screen (19 against 12): 402 readings of the 432 against 342. A single key and a single payload leave a difference of a picture or two to chance: with three keys for this package and three payloads for PixelSeal (72 markings of the 24 photographs each; SSIMULACRA2 84.0 against 83.9, butteraugli 1.80 against 1.78), the blind mark reads more after WebP 60 (67 against 64), JPEG 50 and 30 (72 and 72 against 70 and 56), noise (72 against 68), crops (51 and 30 against 1 and 0), a turn by 30° (72 against 0) and a photograph of a screen (61 against 37), as many after the other attacks, a turn by 3° and perspective (72) among them, but 70% and JPEG 80 (71 against 72): 1216 readings of 1296 against 1016. Made less visible than PixelSeal by both measures (`Strength: 0.71`: SSIMULACRA2 85.1 against 83.9, butteraugli 1.68 against 1.78), it still reads as many or more after every attack but 70% and JPEG 80 (23 against 24): 403 against 342; with three keys (85.0 and 1.70), 1209 against 1016, fewer only after 70% and JPEG 80 (71 against 72), after WebP 60 66 against 64. On the first set, about as visible by SSIMULACRA2 (`Strength: 0.73`, 85.3 against 85.1) and more by butteraugli (2.5 against 2.1), 78 of 90 against 65. WAM, which finds marks in parts of a picture, reads most crops to 40% (22 of 24) but none turned by 30°, and fewer after JPEG 30 and WebP 60 (10 and 14). Video Seal 1.0, PixelSeal's forerunner, reads 4 of the 5 untouched pictures of the first set and as many as PixelSeal or fewer after every attack. ChunkySeal, made to carry four times as many bits, survives less: after JPEG 50 and 30 it reads 11 and 7 of the Kodak photographs, after WebP 60 4. They are networks run with PyTorch, their weights downloads of 0.2 GB (Video Seal), 0.4 GB (WAM), 1.2 GB (PixelSeal) and 13 GB (ChunkySeal); the marks here need nothing but this package.

MBRS, CIN and HiDDeN, networks often compared against in research, were run as Video Seal's evaluation packages them (MBRS carries 256 bits, which hold the 9 bytes three times over as for PixelSeal; CIN carries 30 and HiDDeN 48, which hold the first bytes). They too take the whole picture as it is, and read nothing cropped, turned, in perspective or photographed off a screen. MBRS reads every JPEG of the first set (5 / 5 / 5 / 5, against 5 / 5 / 4 / 4 for the blind mark at its default), but changes the picture more (SSIMULACRA2 75.5 against 79.8, butteraugli 6.4 against 3.3; on the Kodak photographs 76.7 and 5.5 against 78.0 and 2.4, where it reads fewer after JPEG 30 and WebP 60, 22 and 20 against 24 and 24). HiDDeN, at the settings of its authors' model, changes the picture far more and reads 10 of the 24 Kodak photographs untouched. CIN, more visible than MBRS (SSIMULACRA2 67.0 on the first set, butteraugli 9.3), reads fewer after JPEG 30 than the blind mark at its default (12 of the Kodak photographs against 24).

Two more networks, of 2024 and 2025, carry 100 bits without a code against errors; here they hold the 9 bytes, scrambled by a fixed random mask, with 28 bits of BCH that put right 4 wrong bits, as InvisMark's authors' code adds. Microsoft's [InvisMark](https://github.com/microsoft/InvisMark), which marks a copy made 256 × 256 and scales the difference back, is the faintest of all here (on the Kodak photographs PSNR 50.4, SSIMULACRA2 90.9, butteraugli 1.5) and survives the least: 6 of the 24 after JPEG 70, none after JPEG 50, WebP 60, 70% and JPEG 80, crops, a turn by 30° or a photograph of a screen. As faint by SSIMULACRA2 (`Strength: 0.44`: 90.9 against 90.9; butteraugli 1.06 against 1.48), the blind mark reads 308 readings of the 432 against 222: more after JPEG 70, 50 and 30 (22, 19 and 8 against 6, 0 and 0), WebP 60 (3 against 0), 70% and JPEG 80 (15 against 0), crops to 40% (12 against 0), a turn by 30° (20 against 0), perspective (21 against 4) and a photograph of a screen (4 against 0); as many untouched, after darker and more contrast and after a crop to 25% (none), one to three fewer after JPEG 90, halving, a turn by 3°, noise and a screenshot (21 to 23 against 24). [VINE](https://github.com/Shilin-LU/VINE) (ICLR 2025) makes an image generator (SD-Turbo) a watermark encoder, to survive editing by other generators; VINE-R, measured here, marks a copy made 256 × 256 as InvisMark does. It survives every recompression and resizing (24 of 24 down to JPEG 30 and WebP 60), but changes the picture far more than the others (SSIMULACRA2 46.2 on the Kodak photographs, −8.1 on the first set; its generator paints over the thin black border of some of the photographs, butteraugli 60.6), and reads nothing cropped, turned, in perspective or photographed off a screen. Less visible (`Strength: 1.5`, SSIMULACRA2 65.0), the blind mark here reads as many after recompression and resizing, and after crops, turns, perspective and photographs of a screen too (21 to 24 of 24). Their weights are downloads of 1.1 GB (InvisMark) and 4.4 GB (VINE-R, and SD-Turbo's text encoder besides). [RivaGAN](https://github.com/DAI-Lab/RivaGAN) (2019), the network that invisible-watermark ships, carries 32 bits (here the first 4 bytes, all of which must come back). About as visible as the blind mark at its default on the Kodak photographs (SSIMULACRA2 77.7 against 78.0, butteraugli 2.1 against 2.4), it reads 23 of them untouched, 2 after JPEG 30, 9 after WebP 60, and none halved, cropped or turned by 30°. [StegaStamp](https://github.com/tancik/StegaStamp) (2020), made to be read from photographs of prints, was run as ISPRAS's WIBE packages its authors' model (100 bits, with the same BCH as InvisMark): it survives recompression, resizing, perspective and a photograph of a screen (24 of 24 each), but reads nothing cropped or turned, and is far more visible than any mark here (PSNR 29.7, SSIMULACRA2 −17.6, butteraugli 22.7 on the Kodak photographs). The screen mark here, far less visible (SSIMULACRA2 33.6, butteraugli 6.5), reads as many after those attacks, and after turns and crops to 40% too (24 and 22).

Everyday processing, done with this package's own operations, left every blind mark at its default readable, on the five pictures above with three keys (15 marks) and on the 24 Kodak photographs: 1.5 times larger, grey, saturation halved, hue turned by 60°, JPEG 80 three times over, 256 colours, sharpening, gamma 0.8, a 3 × 3 median, a blur of 1 or 2, normalising, turned by 90°, mirrored, twice larger then JPEG 80, the longer side to 1080 then JPEG 85. Harder ones read fewer (of 15 and of 24): a 5 × 5 median, 15 and 22; JPEG 50 three times over, 12 and 24; the middle half enlarged to the whole, 12 and 22; reduced to a third, 15 and 23 (the Kodak photographs become 256 × 170, where the mark's details are smaller than a pixel).

A harder simulation of a phone's photograph, done with this package's own operations, has stronger perspective and Pillow's sharpening. There the screen mark is read from all three photographs and not from the small photograph or the screenshot of a user interface; leaving any one of its steps out, from 3 to 5 of the 5.

## Limits

- **Visibility and robustness trade off.** A mark that survives a camera is a faint texture up close in flat areas. One that cannot be seen at all survives less. `Strength` moves along that line: the blind mark at 1 suits pictures that stay digital: here it was read from the photographs saved again as JPEG down to quality 30, and as WebP at 60 from three of the five pictures (WebP's filters take more of it); use 1.5 to 2 for harder recompression and for flat pictures; the screen mark is for what may be photographed.
- **Flat pictures carry less.** A user interface or a document, mostly white, has few places to hide the mark. Its flat areas carry it weakly, so raise `Strength` for such pictures.
- **Small pictures and small crops carry fewer repeats.** A picture of 640×360 or less is read more poorly after heavy damage. The blind mark of a small photograph has details of a pixel, so that it repeats enough: the 24 Kodak photographs made 384×256 or 256×171 are all read as they are and after JPEG 80 and WebP 80, made 192×128 24, 23 and 24; after JPEG 50 or WebP 60 23 and 20 at 384×256, 6 and 18 at 256×171. A smooth small picture keeps details of 2 pixels, which WebP and JPEG keep better there. A crop must keep about two repeats each way: raise `Density` when crops are likely, which also makes the mark's details finer.
- **Photographs of a screen.** Crop the photograph to the screen, and keep the angle moderate. The simulations above are what is measured; real phones, screens and distances vary.
- **It is not a lock.** Someone who knows the mark is there can still damage it beyond reading (heavy noise, a strong blur, re-drawing the picture). The key keeps others from reading the mark or making a false one, not from destroying it.
