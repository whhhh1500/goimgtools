package inspect

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Exif is what a camera, a phone or a program recorded about a picture in
// its EXIF block: the fields people look for. A field the file does not
// have is left at its zero value.
type Exif struct {
	// Make and Model name the camera or phone; Lens the lens, when the
	// camera wrote it; Software the program that wrote the file.
	Make, Model, Lens, Software string
	// Artist and Copyright are free text.
	Artist, Copyright string
	// Taken is when the picture was taken, by the clock of the camera.
	// Most cameras do not record a time zone: the time is then given in
	// UTC, meaning "this wall-clock time, zone unknown". HasTaken tells
	// whether there is one.
	Taken    time.Time
	HasTaken bool
	// Orientation is how the picture has to be turned to be upright, 1
	// (as it is) to 8; 0 when the file does not say. Image.AutoOrient()
	// applies it.
	Orientation int
	// Exposure is the exposure time in seconds (0.004 for 1/250 s),
	// Aperture the f-number (2.8 for f/2.8), ISO the sensitivity,
	// FocalLength the focal length in millimetres.
	Exposure    float64
	Aperture    float64
	ISO         int
	FocalLength float64
	// HasGPS tells whether the picture carries a position. Latitude and
	// Longitude are in degrees, north and east positive.
	HasGPS              bool
	Latitude, Longitude float64
	// Altitude is in metres above sea level (negative below), when
	// HasAltitude is set.
	HasAltitude bool
	Altitude    float64
}

// String puts what is known in a line of words.
func (e Exif) String() string {
	var parts []string
	add := func(format string, args ...any) { parts = append(parts, fmt.Sprintf(format, args...)) }
	if name := strings.TrimSpace(e.Make + " " + e.Model); name != "" {
		// Many cameras repeat the make in the model.
		if e.Make != "" && strings.HasPrefix(strings.ToLower(e.Model), strings.ToLower(e.Make)) {
			name = e.Model
		}
		add("%s", name)
	}
	if e.Lens != "" {
		add("%s", e.Lens)
	}
	if e.HasTaken {
		add("taken %s", e.Taken.Format("2006-01-02 15:04:05"))
	}
	if e.Exposure > 0 {
		if e.Exposure < 1 {
			add("1/%.0f s", 1/e.Exposure)
		} else {
			add("%g s", e.Exposure)
		}
	}
	if e.Aperture > 0 {
		add("f/%g", e.Aperture)
	}
	if e.ISO > 0 {
		add("ISO %d", e.ISO)
	}
	if e.FocalLength > 0 {
		add("%g mm", e.FocalLength)
	}
	if e.HasGPS {
		pos := fmt.Sprintf("at %.6f, %.6f", e.Latitude, e.Longitude)
		if e.HasAltitude {
			pos += fmt.Sprintf(", %.0f m", e.Altitude)
		}
		add("%s", pos)
	}
	if e.Orientation > 1 {
		add("orientation %d", e.Orientation)
	}
	if e.Software != "" {
		add("written by %s", e.Software)
	}
	if e.Artist != "" {
		add("by %s", e.Artist)
	}
	if e.Copyright != "" {
		add("%s", e.Copyright)
	}
	if len(parts) == 0 {
		return "an EXIF block with none of the usual fields"
	}
	return strings.Join(parts, ", ")
}

// ReadExif returns what the EXIF block of an image says, and false when
// the image has none. Only the header of the file is read.
//
// Whether a format's EXIF is read at all is the library's matter: at
// present JPEG, PNG, WebP and TIFF (whose first directory holds it). What a block says about position is as private as
// the place it names; the library drops the block from what it writes
// unless KeepExif asks for it.
func ReadExif(img *goimgtools.Image) (Exif, bool, error) {
	if img == nil {
		return Exif{}, false, errors.New("goimgtools/inspect: ReadExif needs an image")
	}
	// The stream of a recipe carries the metadata it would write; asking
	// for it reads the header and no pixel.
	src, err := img.Clone().KeepExif().Stream(nil)
	if err != nil {
		return Exif{}, false, err
	}
	data := pipeline.MetaOf(src).Exif
	src.Close()
	if len(data) == 0 {
		return Exif{}, false, nil
	}
	e, err := ParseExif(data)
	if err != nil {
		return Exif{}, false, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	return e, true, nil
}

// EXIF tags, by the directory they are in.
const (
	tagMake        = 0x010f
	tagModel       = 0x0110
	tagOrientation = 0x0112
	tagSoftware    = 0x0131
	tagDateTime    = 0x0132
	tagArtist      = 0x013b
	tagCopyright   = 0x8298
	tagExifIFD     = 0x8769
	tagGPSIFD      = 0x8825

	tagExposureTime     = 0x829a
	tagFNumber          = 0x829d
	tagISO              = 0x8827
	tagDateTimeOriginal = 0x9003
	tagOffsetOriginal   = 0x9011
	tagFocalLength      = 0x920a
	tagLensModel        = 0xa434

	tagGPSLatRef = 1
	tagGPSLat    = 2
	tagGPSLonRef = 3
	tagGPSLon    = 4
	tagGPSAltRef = 5
	tagGPSAlt    = 6
)

// An EXIF block is a small TIFF file: directories of twelve-byte entries,
// each a tag, a type, a count and either the value itself or where it is.
type exifReader struct {
	data []byte
	bo   binary.ByteOrder
}

// entry is one directory entry with its value located.
type entry struct {
	tag, typ uint16
	count    uint32
	value    []byte // count values of the type's size
}

// typeSize is the size in bytes of one value of an EXIF type, 0 for types
// that are not known.
func typeSize(t uint16) uint32 {
	switch t {
	case 1, 2, 6, 7: // BYTE, ASCII, SBYTE, UNDEFINED
		return 1
	case 3, 8: // SHORT, SSHORT
		return 2
	case 4, 9, 11: // LONG, SLONG, FLOAT
		return 4
	case 5, 10, 12: // RATIONAL, SRATIONAL, DOUBLE
		return 8
	}
	return 0
}

// maxExifEntries bounds a directory: real ones have a few dozen entries.
const maxExifEntries = 1024

// dir reads the directory at offset off. Entries that point outside the
// block or have a type that is not known are left out; a directory that
// itself does not fit is an error.
func (r *exifReader) dir(off uint32) ([]entry, error) {
	if uint64(off)+2 > uint64(len(r.data)) {
		return nil, errors.New("exif: a directory lies outside the block")
	}
	n := int(r.bo.Uint16(r.data[off:]))
	if n > maxExifEntries || uint64(off)+2+uint64(n)*12 > uint64(len(r.data)) {
		return nil, errors.New("exif: a directory does not fit in the block")
	}
	out := make([]entry, 0, n)
	for i := 0; i < n; i++ {
		raw := r.data[int(off)+2+12*i:][:12]
		e := entry{tag: r.bo.Uint16(raw), typ: r.bo.Uint16(raw[2:]), count: r.bo.Uint32(raw[4:])}
		size := uint64(typeSize(e.typ)) * uint64(e.count)
		switch {
		case size == 0:
			continue
		case size <= 4:
			e.value = raw[8 : 8+size]
		default:
			at := uint64(r.bo.Uint32(raw[8:]))
			if at+size > uint64(len(r.data)) {
				continue
			}
			e.value = r.data[at : at+size]
		}
		out = append(out, e)
	}
	return out, nil
}

// text returns an ASCII value without its terminator and padding.
func (e entry) text() string {
	if e.typ != 2 {
		return ""
	}
	s := string(e.value)
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// number returns value i as an integer, for the integer types.
func (r *exifReader) number(e entry, i int) (int64, bool) {
	if i < 0 || uint32(i) >= e.count {
		return 0, false
	}
	switch e.typ {
	case 1, 7:
		return int64(e.value[i]), true
	case 6:
		return int64(int8(e.value[i])), true
	case 3:
		return int64(r.bo.Uint16(e.value[2*i:])), true
	case 8:
		return int64(int16(r.bo.Uint16(e.value[2*i:]))), true
	case 4:
		return int64(r.bo.Uint32(e.value[4*i:])), true
	case 9:
		return int64(int32(r.bo.Uint32(e.value[4*i:]))), true
	}
	return 0, false
}

// real returns value i as a number, for rationals and integers. A
// rational with a zero denominator is no value.
func (r *exifReader) real(e entry, i int) (float64, bool) {
	if i < 0 || uint32(i) >= e.count {
		return 0, false
	}
	switch e.typ {
	case 5:
		num, den := r.bo.Uint32(e.value[8*i:]), r.bo.Uint32(e.value[8*i+4:])
		if den == 0 {
			return 0, false
		}
		return float64(num) / float64(den), true
	case 10:
		num, den := int32(r.bo.Uint32(e.value[8*i:])), int32(r.bo.Uint32(e.value[8*i+4:]))
		if den == 0 {
			return 0, false
		}
		return float64(num) / float64(den), true
	}
	n, ok := r.number(e, i)
	return float64(n), ok
}

// ParseExif reads an EXIF block in its plain form: the bytes that start
// with "II" or "MM" (a leading "Exif\0\0", as JPEG files have it, is
// skipped). It never fails on what single fields hold, only when the block
// as a whole is not EXIF; fields it cannot make sense of are left empty.
func ParseExif(data []byte) (Exif, error) {
	if len(data) >= 6 && string(data[:6]) == "Exif\x00\x00" {
		data = data[6:]
	}
	if len(data) < 8 {
		return Exif{}, errors.New("exif: the block is too short")
	}
	r := &exifReader{data: data}
	switch string(data[:2]) {
	case "II":
		r.bo = binary.LittleEndian
	case "MM":
		r.bo = binary.BigEndian
	default:
		return Exif{}, errors.New("exif: the block does not start with a byte order")
	}
	if r.bo.Uint16(data[2:]) != 42 {
		return Exif{}, errors.New("exif: the block is not in TIFF form")
	}
	first, err := r.dir(r.bo.Uint32(data[4:]))
	if err != nil {
		return Exif{}, err
	}
	var e Exif
	var modified string
	var sub, gps uint32
	for _, f := range first {
		switch f.tag {
		case tagMake:
			e.Make = f.text()
		case tagModel:
			e.Model = f.text()
		case tagSoftware:
			e.Software = f.text()
		case tagArtist:
			e.Artist = f.text()
		case tagCopyright:
			e.Copyright = f.text()
		case tagDateTime:
			modified = f.text()
		case tagOrientation:
			if v, ok := r.number(f, 0); ok && v >= 1 && v <= 8 {
				e.Orientation = int(v)
			}
		case tagExifIFD:
			if v, ok := r.number(f, 0); ok {
				sub = uint32(v)
			}
		case tagGPSIFD:
			if v, ok := r.number(f, 0); ok {
				gps = uint32(v)
			}
		}
	}
	var taken, zone string
	if sub != 0 {
		// A directory that cannot be read costs its fields, not the rest.
		fields, _ := r.dir(sub)
		for _, f := range fields {
			switch f.tag {
			case tagDateTimeOriginal:
				taken = f.text()
			case tagOffsetOriginal:
				zone = f.text()
			case tagLensModel:
				e.Lens = f.text()
			case tagExposureTime:
				if v, ok := r.real(f, 0); ok && v > 0 && !math.IsInf(v, 0) {
					e.Exposure = v
				}
			case tagFNumber:
				if v, ok := r.real(f, 0); ok && v > 0 {
					e.Aperture = v
				}
			case tagFocalLength:
				if v, ok := r.real(f, 0); ok && v > 0 {
					e.FocalLength = v
				}
			case tagISO:
				if v, ok := r.number(f, 0); ok && v > 0 {
					e.ISO = int(v)
				}
			}
		}
	}
	if taken == "" {
		taken = modified // the only date a scanner or an editor writes
	}
	if t, ok := exifTime(taken, zone); ok {
		e.Taken, e.HasTaken = t, true
	}
	if gps != 0 {
		fields, _ := r.dir(gps)
		var lat, lon, alt float64
		var haveLat, haveLon bool
		latRef, lonRef, below := "N", "E", false
		for _, f := range fields {
			switch f.tag {
			case tagGPSLatRef:
				latRef = strings.ToUpper(f.text())
			case tagGPSLonRef:
				lonRef = strings.ToUpper(f.text())
			case tagGPSLat:
				lat, haveLat = r.degrees(f)
			case tagGPSLon:
				lon, haveLon = r.degrees(f)
			case tagGPSAltRef:
				if v, ok := r.number(f, 0); ok {
					below = v == 1
				}
			case tagGPSAlt:
				if v, ok := r.real(f, 0); ok {
					alt, e.HasAltitude = v, true
				}
			}
		}
		if haveLat && haveLon && math.Abs(lat) <= 90 && math.Abs(lon) <= 180 {
			if latRef == "S" {
				lat = -lat
			}
			if lonRef == "W" {
				lon = -lon
			}
			e.HasGPS, e.Latitude, e.Longitude = true, lat, lon
			if below {
				alt = -alt
			}
			e.Altitude = alt
		} else {
			e.HasAltitude = false
		}
	}
	return e, nil
}

// degrees reads a position as EXIF writes it: degrees, minutes, seconds.
func (r *exifReader) degrees(e entry) (float64, bool) {
	d, ok := r.real(e, 0)
	if !ok {
		return 0, false
	}
	m, _ := r.real(e, 1)
	s, _ := r.real(e, 2)
	v := d + m/60 + s/3600
	return v, !math.IsNaN(v) && !math.IsInf(v, 0)
}

// exifTime reads "2026:05:17 09:30:15", with the zone "+08:00" when the
// camera wrote one.
func exifTime(s, zone string) (time.Time, bool) {
	if len(s) < 19 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006:01:02 15:04:05", s[:19], time.UTC)
	if err != nil || t.Year() < 1800 {
		return time.Time{}, false
	}
	if z, err := time.Parse("-07:00", zone); err == nil {
		_, offset := z.Zone()
		t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.FixedZone(zone, offset))
	}
	return t, true
}

// ReadXmp returns the XMP packet of the image's file (Adobe's metadata, in
// XML: keywords, rating, rights, the history of edits), as sharp's
// metadata().xmp, and whether it has one. It reads the file's header (and
// in WebP the chunks after the image data), not the pixels.
func ReadXmp(img *goimgtools.Image) ([]byte, bool, error) {
	if img == nil {
		return nil, false, errors.New("goimgtools/inspect: ReadXmp needs an image")
	}
	src, err := img.Clone().KeepXmp().Stream(nil)
	if err != nil {
		return nil, false, err
	}
	data := pipeline.MetaOf(src).Xmp
	src.Close()
	return data, len(data) > 0, nil
}
