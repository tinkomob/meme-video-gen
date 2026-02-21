package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math/bits"
	"os"

	"github.com/vitali-fedulov/imagehash2"
	"github.com/vitali-fedulov/images4"
)

const (
	hashNumBuckets = 4
	hashEpsilon    = 0.25
)

func main() {
	image1Path := flag.String("img1", "", "Path to first image")
	image2Path := flag.String("img2", "", "Path to second image")
	flag.Parse()

	if *image1Path == "" || *image2Path == "" {
		log.Fatal("Usage: compare_images -img1 <path1> -img2 <path2>")
	}

	fmt.Printf("Comparing images:\n  Image 1: %s\n  Image 2: %s\n\n", *image1Path, *image2Path)

	// Read files
	data1, err := os.ReadFile(*image1Path)
	if err != nil {
		log.Fatalf("Failed to read image 1: %v", err)
	}

	data2, err := os.ReadFile(*image2Path)
	if err != nil {
		log.Fatalf("Failed to read image 2: %v", err)
	}

	// 1. File hash comparison (identical files)
	hash1 := computeFileHash(data1)
	hash2 := computeFileHash(data2)

	fmt.Printf("1. FILE HASH COMPARISON:\n")
	fmt.Printf("   Image 1 SHA256: %s\n", hash1)
	fmt.Printf("   Image 2 SHA256: %s\n", hash2)

	if hash1 == hash2 {
		fmt.Printf("   Result: ✓ IDENTICAL FILES\n\n")
	} else {
		fmt.Printf("   Result: ✗ Different files\n\n")
	}

	// 2. Perceptual hash comparison (similar images)
	fmt.Printf("2. PERCEPTUAL HASH COMPARISON:\n")

	pHash1, err := computePerceptualHash(data1)
	if err != nil {
		log.Fatalf("Failed to compute perceptual hash for image 1: %v", err)
	}

	pHash2, err := computePerceptualHash(data2)
	if err != nil {
		log.Fatalf("Failed to compute perceptual hash for image 2: %v", err)
	}

	fmt.Printf("   Image 1 pHash: %016x\n", pHash1)
	fmt.Printf("   Image 2 pHash: %016x\n", pHash2)

	// Calculate hamming distance (number of different bits)
	hammingDist := bits.OnesCount64(pHash1 ^ pHash2)
	similarity := 100 - (hammingDist * 100 / 64)

	fmt.Printf("   Hamming Distance: %d bits\n", hammingDist)
	fmt.Printf("   Similarity: %d%%\n", similarity)

	if hammingDist == 0 {
		fmt.Printf("   Result: ✓ PERCEPTUALLY IDENTICAL\n\n")
	} else if hammingDist <= 5 {
		fmt.Printf("   Result: ✓ VERY SIMILAR (difference: %d bits)\n\n", hammingDist)
	} else if hammingDist <= 10 {
		fmt.Printf("   Result: ~ SIMILAR (difference: %d bits)\n\n", hammingDist)
	} else {
		fmt.Printf("   Result: ✗ DIFFERENT (difference: %d bits)\n\n", hammingDist)
	}

	// 3. Summary
	fmt.Printf("SUMMARY:\n")
	if hash1 == hash2 {
		fmt.Printf("  Images are: IDENTICAL (same file)\n")
	} else if hammingDist == 0 {
		fmt.Printf("  Images are: IDENTICAL (same content, different files)\n")
	} else if hammingDist <= 5 {
		fmt.Printf("  Images are: VERY SIMILAR\n")
	} else if hammingDist <= 10 {
		fmt.Printf("  Images are: SIMILAR\n")
	} else {
		fmt.Printf("  Images are: DIFFERENT\n")
	}
}

// computeFileHash returns SHA256 hash of the file
func computeFileHash(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// computePerceptualHash returns perceptual hash using imagehash2
func computePerceptualHash(imageData []byte) (uint64, error) {
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return 0, fmt.Errorf("decode image: %w", err)
	}

	icon := images4.Icon(img)
	centralHash := imagehash2.CentralHash9(icon, hashEpsilon, hashNumBuckets)

	return centralHash, nil
}
