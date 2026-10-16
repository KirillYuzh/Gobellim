package guard

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

type ArtifactHash interface {
	Detector
	ArtifactHash() string
}

const artifactDigestLength = 16

func ArtifactHashes(layers []Detector) string {
	var hashes []string
	for _, d := range layers {
		if d == nil {
			continue
		}
		h, ok := d.(ArtifactHash)
		if !ok {
			continue
		}
		if hash := strings.TrimSpace(h.ArtifactHash()); hash != "" {
			hashes = append(hashes, hash)
		}
	}
	if len(hashes) == 0 {
		return ""
	}
	sort.Strings(hashes)
	sum := sha256.Sum256([]byte(strings.Join(hashes, "\n")))
	return hex.EncodeToString(sum[:])[:artifactDigestLength]
}

func computeVersion(opts Options, layers []layer, artifact string) string {
	version := opts.Version
	if version == "" {
		version = defaultVersion(layers, opts)
	}
	if artifact == "" {
		return version
	}
	if strings.HasSuffix(version, "/"+artifact) {
		return version
	}
	return version + "/" + artifact
}

func defaultVersion(layers []layer, opts Options) string {
	h := sha256.New()
	for _, l := range layers {
		h.Write([]byte(l.name))
		h.Write([]byte{0})
	}
	h.Write([]byte(strconv.Itoa(opts.MaxChars)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(opts.WindowChars)))
	return "derived-" + hex.EncodeToString(h.Sum(nil))[:artifactDigestLength]
}
