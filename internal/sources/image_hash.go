package sources

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math/bits"
	"time"

	"github.com/samber/lo"
	"github.com/vitali-fedulov/imagehash2"
	"github.com/vitali-fedulov/images4"

	"meme-video-gen/internal/model"
)

const (
	// imagehash2 parameters for hash table pre-filtering
	hashNumBuckets = 4
	hashEpsilon    = 0.25
)

// ComputeImageHash computes a perceptual hash for image data.
// Returns the central hash for indexing and similarity comparison.
func (sc *Scraper) ComputeImageHash(imageData []byte) (uint64, error) {
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return 0, fmt.Errorf("decode image: %w", err)
	}

	icon := images4.Icon(img)
	centralHash := imagehash2.CentralHash9(icon, hashEpsilon, hashNumBuckets)

	return centralHash, nil
}

// IsDuplicate checks if an image with similar hash already exists in the index.
// Uses imagehash2 hashes for quick pre-filter check.
func (sc *Scraper) IsDuplicate(imageData []byte, sourcesIdx *model.SourcesIndex) (bool, error) {
	newIcon, err := sc.decodeToIcon(imageData)
	if err != nil {
		return false, fmt.Errorf("decode new image: %w", err)
	}

	// Fast pre-filter: check against central hashes
	newHashSet := imagehash2.HashSet9(newIcon, hashEpsilon, hashNumBuckets)

	for _, existingAsset := range sourcesIdx.Items {
		// Quick hash table lookup - check if any hash from newHashSet matches
		for _, newHash := range newHashSet {
			if newHash == existingAsset.ImageHash {
				sc.logIfNotSilent("image_hash: found potential duplicate with existing source %s", existingAsset.ID)
				return true, nil
			}
		}
	}

	return false, nil
}

// decodeToIcon decodes image bytes and returns the icon for comparison
func (sc *Scraper) decodeToIcon(imageData []byte) (images4.IconT, error) {
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return images4.IconT{}, fmt.Errorf("decode: %w", err)
	}
	icon := images4.Icon(img)
	return icon, nil
}

// CheckImageSimilarity checks if two images are visually similar.
// Useful for testing and validation.
func (sc *Scraper) CheckImageSimilarity(imageData1, imageData2 []byte) (bool, error) {
	icon1, err := sc.decodeToIcon(imageData1)
	if err != nil {
		return false, fmt.Errorf("decode image1: %w", err)
	}

	icon2, err := sc.decodeToIcon(imageData2)
	if err != nil {
		return false, fmt.Errorf("decode image2: %w", err)
	}

	return images4.Similar(icon1, icon2), nil
}

// HammingDistance calculates the Hamming distance between two hashes
func hammingDistance(hash1, hash2 uint64) int {
	xor := hash1 ^ hash2
	return bits.OnesCount64(xor)
}

// IsHashInBlacklist checks if a hash exists in the image hash blacklist.
// Uses an in-memory cache with 5-minute TTL to avoid hitting S3 on every call.
func (sc *Scraper) IsHashInBlacklist(ctx context.Context, hash uint64) (bool, error) {
	if hash == 0 {
		return false, nil
	}

	sc.hashCacheMux.RLock()
	cached := sc.hashBlacklist
	exp := sc.hashBlacklistExp
	sc.hashCacheMux.RUnlock()

	if cached == nil || time.Now().After(exp) {
		// Cache miss or expired — reload from S3
		var index model.ImageHashIndex
		found, err := sc.s3.ReadJSON(ctx, sc.cfg.ImageHashIndexKey, &index)
		if err != nil {
			sc.log.Warnf("image_hash: failed to read blacklist: %v", err)
			return false, nil
		}
		if !found {
			index = model.ImageHashIndex{Hashes: []uint64{}}
		}
		sc.hashCacheMux.Lock()
		sc.hashBlacklist = &index
		sc.hashBlacklistExp = time.Now().Add(5 * time.Minute)
		sc.hashCacheMux.Unlock()
		cached = &index
	}

	return lo.Contains(cached.Hashes, hash), nil
}

// AddHashToBlacklist adds a hash to the image hash blacklist and invalidates the cache.
func (sc *Scraper) AddHashToBlacklist(ctx context.Context, hash uint64) error {
	if hash == 0 {
		return nil
	}

	// Load fresh from S3 for the write (ignore cache to avoid stale overwrites)
	var index model.ImageHashIndex
	found, err := sc.s3.ReadJSON(ctx, sc.cfg.ImageHashIndexKey, &index)
	if err != nil {
		sc.log.Errorf("image_hash: failed to read blacklist for update: %v", err)
		index = model.ImageHashIndex{Hashes: []uint64{}}
	}
	if !found {
		index = model.ImageHashIndex{Hashes: []uint64{}}
	}

	if !lo.Contains(index.Hashes, hash) {
		index.Hashes = append(index.Hashes, hash)
		sc.log.Infof("image_hash: added hash %d to blacklist (total: %d)", hash, len(index.Hashes))
	}

	err = sc.s3.WriteJSON(ctx, sc.cfg.ImageHashIndexKey, &index)
	if err == nil {
		// Invalidate cache so next read picks up the new entry
		sc.hashCacheMux.Lock()
		sc.hashBlacklist = nil
		sc.hashCacheMux.Unlock()
	}
	return err
}
