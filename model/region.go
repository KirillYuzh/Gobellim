package model

// Region names where a client sends prompts. It exists so the core can apply a
// data-residency rule: for a provider outside RegionRU, PII masking is mandatory
// (see kit's BR-106).
type Region string

const (
	RegionRU Region = "ru"
	RegionEU Region = "eu"
	RegionUS Region = "us"
)

// Regional is an optional capability of a Client: a client that can state where
// it runs implements it. A client that does not implement Regional counts as
// "region unspecified" and no residency rule is applied — the same opt-in shape
// as ContextWindow and Streamer.
type Regional interface {
	Region() Region
}
