package tests

import (
	"bytes"
	"image/color"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestPillowTransform: Transform is Pillow's Image.transform, to the byte:
// affine (in fixed point, and a matrix that only scales), extent (within
// the image and beyond it), perspective (one from four corners, as the
// Pillow cookbook works it out, and one that divides by 0 along a line) and
// quad (one that turns the image over); colour, with transparency and grey;
// nearest, bilinear and bicubic; what lies outside left as Pillow fills it
// or in a colour. The sums are of Pillow 12.1.1's pixels. The perspective
// TransformPerspectiveQuad works out from the corners gives the pixels of
// the cookbook's (numpy's) coefficients.
func TestPillowTransform(t *testing.T) {
	for _, c := range []struct {
		in     string
		w, h   int
		t      Transformation
		interp Interpolator
		bg     color.Color
		sum    string
	}{
		{"pillow/source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateNearest, nil, "3e09e3a75b11145a"},
		{"pillow/source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBilinear, nil, "853aa52e7036d197"},
		{"pillow/source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "928bf29171c8626d"},
		{"pillow/source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBicubic, nil, "e4ee3b0ea41062be"},
		{"pillow/source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "14656237ba61fde7"},
		{"pillow/source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateNearest, nil, "ab5fbb9bdd52adfe"},
		{"pillow/source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBilinear, nil, "2af86f504dbe9291"},
		{"pillow/source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "4cfca51a99bbd6e1"},
		{"pillow/source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBicubic, nil, "c2ae7d0ebaa6ead2"},
		{"pillow/source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "8e67f07711fecd0d"},
		{"pillow/source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateNearest, nil, "a7d31bbfe54cefba"},
		{"pillow/source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBilinear, nil, "108620fe00ed8076"},
		{"pillow/source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "108620fe00ed8076"},
		{"pillow/source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBicubic, nil, "5178abafc8f212dc"},
		{"pillow/source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "5178abafc8f212dc"},
		{"pillow/source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateNearest, nil, "5ffbf9a2b98e3539"},
		{"pillow/source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBilinear, nil, "8f7360f27fefd249"},
		{"pillow/source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "f76a5d831cd33ee8"},
		{"pillow/source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBicubic, nil, "31155b95a79e1615"},
		{"pillow/source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "facbfa76a6102af0"},
		{"pillow/source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateNearest, nil, "6edb80faaa947c79"},
		{"pillow/source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBilinear, nil, "f5ae63302f7dc038"},
		{"pillow/source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "f5ae63302f7dc038"},
		{"pillow/source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBicubic, nil, "aa1bca04ed83d305"},
		{"pillow/source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "aa1bca04ed83d305"},
		{"pillow/source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateNearest, nil, "6f6ea2f074c2b993"},
		{"pillow/source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBilinear, nil, "b56b09e592902e66"},
		{"pillow/source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "1406cff8de195b7a"},
		{"pillow/source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBicubic, nil, "155866759bd3e10e"},
		{"pillow/source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "6b0125b1fa983bac"},
		{"pillow/source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateNearest, nil, "73f8c25cfb67282c"},
		{"pillow/source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBilinear, nil, "daa87780dc5a05f3"},
		{"pillow/source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "c1623537e77e7ed4"},
		{"pillow/source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBicubic, nil, "17f91a1dde4047ca"},
		{"pillow/source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "20dbb531d037a321"},
		{"pillow/source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateNearest, nil, "f43f836cbea96696"},
		{"pillow/source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateNearest, color.NRGBA{10, 200, 30, 128}, "f43f836cbea96696"},
		{"pillow/source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBilinear, nil, "ee818a0070b17ba4"},
		{"pillow/source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "ee818a0070b17ba4"},
		{"pillow/source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBicubic, nil, "72de8dd7765b1bdf"},
		{"pillow/source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "72de8dd7765b1bdf"},
		{"pillow/source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateNearest, nil, "46c2ad83471ec993"},
		{"pillow/source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateNearest, color.NRGBA{10, 200, 30, 128}, "99ed84408b769c69"},
		{"pillow/source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBilinear, nil, "c4744221de00ed2f"},
		{"pillow/source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "b708e81edbcb8fdb"},
		{"pillow/source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBicubic, nil, "a3b8c7079471b0f1"},
		{"pillow/source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "35bbc87c620dbc2d"},
		{"pillow/median-rgba-source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateNearest, nil, "e646ba58caf77a63"},
		{"pillow/median-rgba-source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBilinear, nil, "194817702d599fca"},
		{"pillow/median-rgba-source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "6f737ddb84bac38c"},
		{"pillow/median-rgba-source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBicubic, nil, "8865f60c36f8dfcf"},
		{"pillow/median-rgba-source.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "494f7b1c6c0b4dfc"},
		{"pillow/median-rgba-source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateNearest, nil, "86f0610031fb2261"},
		{"pillow/median-rgba-source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBilinear, nil, "de8a2ea6c100b33e"},
		{"pillow/median-rgba-source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "ca0a77c6ae233a7f"},
		{"pillow/median-rgba-source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBicubic, nil, "7875b42124d87a93"},
		{"pillow/median-rgba-source.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "a039137c428ef995"},
		{"pillow/median-rgba-source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateNearest, nil, "65e3af37c7415c44"},
		{"pillow/median-rgba-source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBilinear, nil, "2ca1e9560ef45cff"},
		{"pillow/median-rgba-source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "2ca1e9560ef45cff"},
		{"pillow/median-rgba-source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBicubic, nil, "8fcb926c0098531c"},
		{"pillow/median-rgba-source.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "8fcb926c0098531c"},
		{"pillow/median-rgba-source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateNearest, nil, "ae8dddafbf77ce57"},
		{"pillow/median-rgba-source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBilinear, nil, "0bfffc33f8514af8"},
		{"pillow/median-rgba-source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "9fa8c1976250b345"},
		{"pillow/median-rgba-source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBicubic, nil, "7e8cc8f910deecbb"},
		{"pillow/median-rgba-source.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "eca50e01638863fa"},
		{"pillow/median-rgba-source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateNearest, nil, "c8b97266e533ca9a"},
		{"pillow/median-rgba-source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBilinear, nil, "47d4be70d3df1a36"},
		{"pillow/median-rgba-source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "47d4be70d3df1a36"},
		{"pillow/median-rgba-source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBicubic, nil, "3c6f0f8b188210ed"},
		{"pillow/median-rgba-source.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "3c6f0f8b188210ed"},
		{"pillow/median-rgba-source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateNearest, nil, "a259c820d07d83f2"},
		{"pillow/median-rgba-source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBilinear, nil, "b690168604a67c1f"},
		{"pillow/median-rgba-source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "88e12ea0acc9054b"},
		{"pillow/median-rgba-source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBicubic, nil, "852463d2aba38633"},
		{"pillow/median-rgba-source.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "b5ffbf9b7033c6ea"},
		{"pillow/median-rgba-source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateNearest, nil, "28b25582f711ef5f"},
		{"pillow/median-rgba-source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBilinear, nil, "c2ae16981403c417"},
		{"pillow/median-rgba-source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "4dcbcfee0ccb8005"},
		{"pillow/median-rgba-source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBicubic, nil, "9487ed4a175a3059"},
		{"pillow/median-rgba-source.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "877e282545bcb375"},
		{"pillow/median-rgba-source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateNearest, nil, "bd1c74e5dae0cd21"},
		{"pillow/median-rgba-source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateNearest, color.NRGBA{10, 200, 30, 128}, "bd1c74e5dae0cd21"},
		{"pillow/median-rgba-source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBilinear, nil, "7278277397542696"},
		{"pillow/median-rgba-source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "7278277397542696"},
		{"pillow/median-rgba-source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBicubic, nil, "f31c36c628062728"},
		{"pillow/median-rgba-source.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "f31c36c628062728"},
		{"pillow/median-rgba-source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateNearest, nil, "38a9ab9ae14ac44d"},
		{"pillow/median-rgba-source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateNearest, color.NRGBA{10, 200, 30, 128}, "b12646ccac7c5f71"},
		{"pillow/median-rgba-source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBilinear, nil, "455c76302b7b0ec1"},
		{"pillow/median-rgba-source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBilinear, color.NRGBA{10, 200, 30, 128}, "5b4f5dad95cdb909"},
		{"pillow/median-rgba-source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBicubic, nil, "67bbd99533cdc60e"},
		{"pillow/median-rgba-source.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBicubic, color.NRGBA{10, 200, 30, 128}, "812e68f9c734679c"},
		{"pillow/gray.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateNearest, nil, "49fa95a3fd42830d"},
		{"pillow/gray.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBilinear, nil, "372c23f36bb9024a"},
		{"pillow/gray.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBilinear, color.Gray{120}, "765cf2c34a66f3db"},
		{"pillow/gray.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBicubic, nil, "912475a99a9e040e"},
		{"pillow/gray.png", 160, 90, TransformAffine(0.9, 0.2, -5.0, -0.15, 1.1, 8.0), InterpolateBicubic, color.Gray{120}, "9a06cab34b5062de"},
		{"pillow/gray.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateNearest, nil, "8ef05814f82432fe"},
		{"pillow/gray.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBilinear, nil, "1b8c21acc137f974"},
		{"pillow/gray.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBilinear, color.Gray{120}, "9a9a6f37890afd57"},
		{"pillow/gray.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBicubic, nil, "507f793d81db50cb"},
		{"pillow/gray.png", 200, 70, TransformAffine(0.6, 0.0, -3.0, 0.0, 1.3, 2.5), InterpolateBicubic, color.Gray{120}, "8168beeffedbeea7"},
		{"pillow/gray.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateNearest, nil, "57af3d64e4351940"},
		{"pillow/gray.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBilinear, nil, "9c1b46f9e3ded8f8"},
		{"pillow/gray.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBilinear, color.Gray{120}, "9c1b46f9e3ded8f8"},
		{"pillow/gray.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBicubic, nil, "3a53de7addd08b22"},
		{"pillow/gray.png", 120, 80, TransformExtent(10.0, 5.0, 90.5, 70.25), InterpolateBicubic, color.Gray{120}, "3a53de7addd08b22"},
		{"pillow/gray.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateNearest, nil, "11e7af0c8c320919"},
		{"pillow/gray.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBilinear, nil, "d8bf8e7ff283dc9e"},
		{"pillow/gray.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBilinear, color.Gray{120}, "9d39c2107923926e"},
		{"pillow/gray.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBicubic, nil, "af3bda8e827da3b7"},
		{"pillow/gray.png", 90, 140, TransformExtent(-20.0, -10.0, 150.0, 120.0), InterpolateBicubic, color.Gray{120}, "53d61a0196047254"},
		{"pillow/gray.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateNearest, nil, "e7986e57fa5945cc"},
		{"pillow/gray.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBilinear, nil, "18242b4063c76d1e"},
		{"pillow/gray.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBilinear, color.Gray{120}, "18242b4063c76d1e"},
		{"pillow/gray.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBicubic, nil, "708d0e6fc22cb48f"},
		{"pillow/gray.png", 140, 100, TransformPerspective(1.0164889010167641, -0.05983223984718878, 8.0, 0.05192912311507112, 0.8978847271821278, 5.0, 0.00016076025958926051, 8.388007640561415e-05), InterpolateBicubic, color.Gray{120}, "708d0e6fc22cb48f"},
		{"pillow/gray.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateNearest, nil, "4ce95fa83e0af332"},
		{"pillow/gray.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBilinear, nil, "07ff13f1f40b3425"},
		{"pillow/gray.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBilinear, color.Gray{120}, "d5f36a48838ab037"},
		{"pillow/gray.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBicubic, nil, "8a7d4be96c2f1445"},
		{"pillow/gray.png", 160, 120, TransformPerspective(1.05, 0.08, -4.0, 0.03, 0.97, 6.0, 0.0009, 0.0006), InterpolateBicubic, color.Gray{120}, "91f0303ec9adabf4"},
		{"pillow/gray.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateNearest, nil, "9da4728a07673f80"},
		{"pillow/gray.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBilinear, nil, "c071dd8d68e63402"},
		{"pillow/gray.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBilinear, color.Gray{120}, "a78166bd4f6d98dd"},
		{"pillow/gray.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBicubic, nil, "4c19aecd8a6500fb"},
		{"pillow/gray.png", 100, 100, TransformPerspective(1.0, 0.0, 0.0, 0.0, 1.0, 0.0, -0.01, 0.0), InterpolateBicubic, color.Gray{120}, "23f05822c109c748"},
		{"pillow/gray.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateNearest, nil, "325e13767fa1760b"},
		{"pillow/gray.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateNearest, color.Gray{120}, "325e13767fa1760b"},
		{"pillow/gray.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBilinear, nil, "19673dc1c2c11af9"},
		{"pillow/gray.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBilinear, color.Gray{120}, "19673dc1c2c11af9"},
		{"pillow/gray.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBicubic, nil, "2762c4fc38f94de6"},
		{"pillow/gray.png", 130, 90, TransformQuad(5.0, 3.0, 2.0, 97.0, 146.0, 101.0, 142.0, 6.0), InterpolateBicubic, color.Gray{120}, "2762c4fc38f94de6"},
		{"pillow/gray.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateNearest, nil, "9952b31ed2f82e5f"},
		{"pillow/gray.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateNearest, color.Gray{120}, "36538994e50236ae"},
		{"pillow/gray.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBilinear, nil, "a1002a5f3d9fefaa"},
		{"pillow/gray.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBilinear, color.Gray{120}, "6fd4efe8bb693827"},
		{"pillow/gray.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBicubic, nil, "99ff419f1f28f1a7"},
		{"pillow/gray.png", 110, 120, TransformQuad(149.0, 0.0, 170.0, 102.0, -15.0, 113.0, 0.5, 0.25), InterpolateBicubic, color.Gray{120}, "f7ae64438b125ca9"},
		{"pillow/source.png", 140, 100, TransformPerspectiveQuad(8, 5, 2, 94, 140, 99, 147, 12), InterpolateNearest, nil, "6edb80faaa947c79"},
		{"pillow/source.png", 140, 100, TransformPerspectiveQuad(8, 5, 2, 94, 140, 99, 147, 12), InterpolateBilinear, nil, "f5ae63302f7dc038"},
		{"pillow/source.png", 140, 100, TransformPerspectiveQuad(8, 5, 2, 94, 140, 99, 147, 12), InterpolateBicubic, nil, "aa1bca04ed83d305"},
	} {
		for _, threads := range []int{1, 4} {
			o := TransformOptions{Background: c.bg, Interpolator: c.interp}
			out, err := FromBytes(sourcePNG(t, c.in)).Threads(threads).Transform(c.w, c.h, c.t, o).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s %dx%d %+v %+v, %d threads: %s, Pillow's is %s", c.in, c.w, c.h, c.t, o, threads, got, c.sum)
				break
			}
		}
	}
	// By name, and the words of the steps.
	in := sourcePNG(t, "pillow/median-rgba-source.png")
	tr := TransformPerspective(1.05, 0.08, -4, 0.03, 0.97, 6, 0.0009, 0.0006)
	o := TransformOptions{Background: color.NRGBA{10, 200, 30, 128}, Interpolator: InterpolateBicubic}
	want := must(FromBytes(in).Transform(160, 120, tr, o).PNG().ToBytes())
	var words []string
	got := must(FromBytes(in).Do("transform", "160", "120", "perspective", "1.05", "0.08", "-4", "0.03", "0.97", "6", "0.0009", "0.0006", "bicubic", "#0ac81e80").Around(func(s Step, src pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		words = append([]string{s.Name}, s.Args...)
		return run(src)
	}).PNG().ToBytes())
	if !bytes.Equal(got, want) {
		t.Error("transform by name: another image")
	}
	again := must(FromBytes(in).Do(words[0], words[1:]...).PNG().ToBytes())
	if !bytes.Equal(again, want) {
		t.Errorf("the words %v give another image", words)
	}
	// The perspective of corners by name, and its words.
	words = nil
	q := must(FromBytes(in).Transform(90, 60, TransformPerspectiveQuad(3, 4, 1, 90, 140, 99, 120, 2)).PNG().ToBytes())
	byName := must(FromBytes(in).Do("transform", "90", "60", "perspective-quad", "3", "4", "1", "90", "140", "99", "120", "2").Around(func(s Step, src pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		words = append([]string{s.Name}, s.Args...)
		return run(src)
	}).PNG().ToBytes())
	if !bytes.Equal(q, byName) || !bytes.Equal(must(FromBytes(in).Do(words[0], words[1:]...).PNG().ToBytes()), q) {
		t.Errorf("perspective-quad by name, words %v: another image", words)
	}
	for _, args := range [][]string{{"10", "10", "affine", "1", "0", "0", "0", "1"}, {"10", "10", "spin", "1"}, {"0", "10", "extent", "0", "0", "5", "5"},
		{"10", "10", "extent", "0", "0", "5", "5", "nearest", "bicubic"}, {"10", "10", "extent", "0", "0", "5", "5", "red", "blue"}, {"10", "10", "extent", "0", "0", "5", "x"}} {
		if _, err := FromBytes(in).Do("transform", args...).PNG().ToBytes(); err == nil {
			t.Errorf("transform %v accepted", args)
		}
	}
	for _, c := range []struct {
		w, h int
		t    Transformation
		o    TransformOptions
	}{{10, 10, Transformation{}, TransformOptions{}}, {0, 10, TransformExtent(0, 0, 1, 1), TransformOptions{}},
		{10, 10, TransformQuad(0, 0, 0, 1, 1, 1, 1, math.Inf(1)), TransformOptions{}}, {10, 10, TransformExtent(0, 0, 1, 1), TransformOptions{Interpolator: 9}},
		{10, 10, TransformPerspectiveQuad(0, 0, 0, 10, 0, 20, 5, 5), TransformOptions{}}, {10, 10, TransformPerspectiveQuad(0, 0, 0, 10, 10, 10, 5, 5), TransformOptions{}}} {
		if _, err := FromBytes(in).Transform(c.w, c.h, c.t, c.o).PNG().ToBytes(); err == nil {
			t.Errorf("Transform(%d, %d, %+v, %+v) accepted", c.w, c.h, c.t, c.o)
		}
	}
	// The size is known without the pixels.
	if w, h, err := FromBytes(in).Transform(77, 33, TransformExtent(0, 0, 10, 10)).OutputSize(); err != nil || w != 77 || h != 33 {
		t.Errorf("OutputSize %dx%d, %v", w, h, err)
	}
}
