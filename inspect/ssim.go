package inspect

import (
	"errors"
	"fmt"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/metric"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// SSIM is the structural similarity of b to a (Wang et al., 2004), the
// usual measure of how alike two pictures look, 1 for identical ones and
// less the more their local brightness, contrast and structure differ:
// above about 0.98 a difference is hard to see. It is that of the
// reference: on the brightness of the pixels (Rec. 601 luma, laid on white
// where they are transparent), through an 11 by 11 Gaussian window of
// sigma 1.5, with the reference's constants, averaged over every place the
// window fits (a picture smaller than it is taken whole). The images must
// be the same size. Both are read a few rows at a time, in the memory
// limit of a; their operations are run first.
func SSIM(a, b *goimgtools.Image) (float64, error) {
	if a == nil || b == nil {
		return 0, errors.New("goimgtools/inspect: SSIM needs two images")
	}
	sa, err := a.Clone().Stream(nil)
	if err != nil {
		return 0, err
	}
	defer func() { sa.Close() }()
	sb, err := b.Clone().Stream(pipeline.EnvOf(sa))
	if err != nil {
		return 0, fmt.Errorf("second image: %w", err)
	}
	defer func() { sb.Close() }()
	return metric.SSIM(sa, sb)
}
