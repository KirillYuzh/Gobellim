package gliner

type Report struct {
	Compared int
	Agreed   int
	Failures []string
}

func (r Report) Agreement() float64 {
	if r.Compared == 0 {
		return 0
	}
	return float64(r.Agreed) / float64(r.Compared)
}
