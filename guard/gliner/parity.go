package gliner

// Report summarises a parity run: how many fixtures were compared and agreed,
// and the failures.
type Report struct {
	Compared int
	Agreed   int
	Failures []string
}

// Agreement returns Agreed/Compared, or 0 when nothing was compared.
func (r Report) Agreement() float64 {
	if r.Compared == 0 {
		return 0
	}
	return float64(r.Agreed) / float64(r.Compared)
}
