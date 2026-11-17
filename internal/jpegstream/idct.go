package jpegstream

// The inverse DCTs of a sequential scan, on helpers: the blocks of an MCU
// row are decoded into jobs (the entropy decoding is sequential), and once
// the row is decoded its jobs are shared out among the helpers, which make
// its pixels while the next row is decoded. Each job writes its own block of
// the band, so the order does not matter; the band is complete once the
// last row's jobs are done (idctDone), before it is converted.

// idctJob is a block to make into pixels: its coefficients, where it goes
// in the band and of which component.
type idctJob struct {
	b      block
	bx, by int32
	comp   uint8
	dcOnly bool
}

// idctJobBytes is what a job takes, counted in the budget.
const idctJobBytes = 4*blockSize + 16

// startIDCT has the inverse DCTs of a sequential scan done on helpers, when
// there are two helpers or more (with one, the decoding gains nothing: the
// rows of blocks go through memory instead of the cache) and the budget has
// room for two MCU rows of blocks.
func (s *stream) startIDCT() {
	d := s.d
	if d.idct != nil || s.env == nil || s.env.Helpers() < 2 {
		return
	}
	n := 0
	for i := 0; i < d.nComp; i++ {
		n += d.mxx * d.comp[i].h * d.comp[i].v
	}
	need := 2 * int64(n) * idctJobBytes
	if !s.env.Budget.TryReserve(need) {
		return
	}
	s.reserved += need
	d.jobs[0], d.jobs[1] = make([]idctJob, 0, n), make([]idctJob, 0, n)
	d.idct = s.env
}

// idctRow hands the jobs of the MCU row just decoded to the helpers, once
// those of the row before are done (their buffer is filled next).
func (d *decoder) idctRow() error {
	if err := d.idctDone(); err != nil {
		return err
	}
	jobs := d.jobs[d.jobCur]
	d.jobCur ^= 1
	d.jobs[d.jobCur] = d.jobs[d.jobCur][:0]
	env := d.idct
	parts := max(1, min(env.Helpers(), len(jobs)/64))
	errs := make([]error, parts)
	wait := env.Go(func() {
		env.Parallel(parts, func(p int) {
			for i := len(jobs) * p / parts; i < len(jobs)*(p+1)/parts; i++ {
				j := &jobs[i]
				if err := d.reconstructBlock(&j.b, int(j.bx), int(j.by), int(j.comp), j.dcOnly); err != nil && errs[p] == nil {
					errs[p] = err
				}
			}
		})
	})
	d.idctWait = func() error {
		wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// idctDone waits for the jobs handed out.
func (d *decoder) idctDone() error {
	if d.idctWait == nil {
		return nil
	}
	w := d.idctWait
	d.idctWait = nil
	return w()
}
