package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"

	"github.com/KirillYuzh/Gobellim/internal/cache"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

const cacheKeyDomain = "kit/embed/cache/v1"

// CacheConfig configures NewCached. Size is the maximum number of vectors kept;
// zero or negative disables caching.
type CacheConfig struct {
	Size int
}

type cacheRole string

const (
	roleQuery   cacheRole = "query"
	rolePassage cacheRole = "passage"
)

// CachedEmbedder wraps an Embedder with an LRU vector cache keyed by role, model,
// and text, so query and passage embeddings never share an entry.
type CachedEmbedder struct {
	inner Embedder
	cache *cache.LRU[[]float32]
}

var _ Closer = (*CachedEmbedder)(nil)

// NewCached wraps inner with an LRU cache of cfg.Size vectors. It returns
// ErrInvalidConfig when inner is nil and skips caching when Size is not positive.
func NewCached(inner Embedder, cfg CacheConfig) (*CachedEmbedder, error) {
	if inner == nil {
		return nil, fmt.Errorf("embed: cache: Embedder is required: %w", core.ErrInvalidConfig)
	}
	c := &CachedEmbedder{inner: inner}
	if cfg.Size > 0 {
		c.cache = cache.New[[]float32](cfg.Size, 0)
	}
	return c, nil
}

// Model returns the wrapped embedder's model name.
func (c *CachedEmbedder) Model() string { return c.inner.Model() }

// Dim returns the wrapped embedder's vector dimension.
func (c *CachedEmbedder) Dim() int { return c.inner.Dim() }

// Close releases the wrapped embedder's resources. It delegates to the inner
// embedder when that embedder implements Closer, and does nothing otherwise:
// the cache itself owns no resource. Without this the wrapped ONNX session
// pool would stay open for the lifetime of the process.
func (c *CachedEmbedder) Close() error {
	if closer, ok := c.inner.(Closer); ok {
		return closer.Close()
	}
	return nil
}

// Len reports the number of cached vectors; it is zero when caching is disabled.
func (c *CachedEmbedder) Len() int {
	if c.cache == nil {
		return 0
	}
	return c.cache.Len()
}

// Query returns embeddings for search queries, serving and populating the query
// cache.
func (c *CachedEmbedder) Query(ctx context.Context, texts []string) ([][]float32, error) {
	return c.embed(ctx, texts, roleQuery, c.inner.Query)
}

// Passages returns embeddings for document passages, serving and populating the
// passage cache.
func (c *CachedEmbedder) Passages(ctx context.Context, texts []string) ([][]float32, error) {
	return c.embed(ctx, texts, rolePassage, c.inner.Passages)
}

func (c *CachedEmbedder) embed(
	ctx context.Context,
	texts []string,
	role cacheRole,
	call func(context.Context, []string) ([][]float32, error),
) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if c.cache == nil {
		return call(ctx, texts)
	}

	model := c.inner.Model()
	keys := make([]string, len(texts))
	uniqKey := make([]string, 0, len(texts))
	uniqText := make([]string, 0, len(texts))
	uniqAt := make(map[string]int, len(texts))
	for i, t := range texts {
		k := cacheKey(role, model, t)
		keys[i] = k
		if _, ok := uniqAt[k]; ok {
			continue
		}
		uniqAt[k] = len(uniqKey)
		uniqKey = append(uniqKey, k)
		uniqText = append(uniqText, t)
	}

	vectors := make([][]float32, len(uniqKey))
	missText := make([]string, 0, len(uniqKey))
	missKey := make([]string, 0, len(uniqKey))
	missAt := make([]int, 0, len(uniqKey))
	for i, k := range uniqKey {
		if v, ok := c.cache.Get(k); ok {
			vectors[i] = v
			continue
		}
		missText = append(missText, uniqText[i])
		missKey = append(missKey, k)
		missAt = append(missAt, i)
	}

	if len(missText) > 0 {
		vecs, err := call(ctx, missText)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(missText) {
			return nil, fmt.Errorf("embed: cache: embedder returned %d vectors for %d texts: %w",
				len(vecs), len(missText), core.ErrProvider)
		}
		for j, at := range missAt {
			vectors[at] = vecs[j]
			if vecs[j] == nil {
				continue
			}
			c.cache.Put(missKey[j], cloneVector(vecs[j]))
		}
	}

	out := make([][]float32, len(texts))
	for i, k := range keys {
		out[i] = cloneVector(vectors[uniqAt[k]])
	}
	return out, nil
}

func cacheKey(role cacheRole, model, text string) string {
	h := sha256.New()
	writeCacheField(h, cacheKeyDomain)
	writeCacheField(h, string(role))
	writeCacheField(h, model)
	writeCacheField(h, text)
	return hex.EncodeToString(h.Sum(nil))
}

func writeCacheField(h hash.Hash, s string) {
	var n [binary.MaxVarintLen64]byte
	_, _ = h.Write(binary.AppendUvarint(n[:0], uint64(len(s))))
	_, _ = h.Write([]byte(s))
}

func cloneVector(v []float32) []float32 {
	if v == nil {
		return nil
	}
	out := make([]float32, len(v))
	copy(out, v)
	return out
}
