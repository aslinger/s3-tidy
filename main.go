package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
)

// Global Flags
var (
	bucketName string
	days       int
	dryRun     bool
	reportOnly bool
)

// Constants for FinOps (Standard S3 Standard pricing approx $0.023/GB)
const pricePerGB = 0.023

func main() {
	var rootCmd = &cobra.Command{
		Use:   "s3-tidy",
		Short: "Cloud governance tool for S3 cleanup",
		Long:  `A staff-level utility to enforce retention policies and estimate cost savings on stale S3 artifacts.`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Please use the 'scan' command. Try 's3-tidy scan --help'")
		},
	}

	var scanCmd = &cobra.Command{
		Use:   "scan",
		Short: "Scan bucket for stale objects",
		Run: func(cmd *cobra.Command, args []string) {
			runScan(bucketName, days, dryRun, reportOnly)
		},
	}

	// Flag definition
	scanCmd.Flags().StringVarP(&bucketName, "bucket", "b", "", "Target S3 bucket name (required)")
	scanCmd.Flags().IntVarP(&days, "days", "d", 30, "Age threshold in days")
	scanCmd.Flags().BoolVar(&dryRun, "dry-run", true, "Simulate deletion without taking action")
	scanCmd.Flags().BoolVar(&reportOnly, "report", false, "Generate a cost-savings report without deleting")

	scanCmd.MarkFlagRequired("bucket")

	rootCmd.AddCommand(scanCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runScan(bucket string, days int, isDryRun bool, isReport bool) {
	ctx := context.TODO()

	// 1. Load AWS Config (Auto-detects SSO, Env Vars, or ~/.aws/credentials)
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("❌ Unable to load SDK config: %v", err)
	}
	client := s3.NewFromConfig(cfg)

	// 2. Define the cutoff
	cutoff := time.Now().AddDate(0, 0, -days)
	fmt.Printf("🔍 Scanning 's3://%s' for objects older than %s (%d days)...\n", bucket, cutoff.Format("2006-01-02"), days)

	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})

	var staleCount int
	var totalSize int64
	var deletedCount int

	// 3. Pagination Loop
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Fatalf("❌ Failed to list objects: %v", err)
		}

		for _, obj := range page.Contents {
			if obj.LastModified.Before(cutoff) {
				staleCount++

				// FIX: Dereference the pointer (*obj.Size)
				if obj.Size != nil {
					totalSize += *obj.Size
				}

				if isReport {
					continue
				}

				if isDryRun {
					// FIX: Dereference here too
					sizeMB := 0.0
					if obj.Size != nil {
						sizeMB = float64(*obj.Size) / 1024 / 1024
					}
					fmt.Printf("[DRY RUN] Would delete: %s (%s, %.2f MB)\n", *obj.Key, obj.LastModified.Format(time.RFC3339), sizeMB)
				} else {
					// Actual Deletion Logic
					_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
						Bucket: aws.String(bucket),
						Key:    obj.Key,
					})
					if err != nil {
						log.Printf("⚠️ Failed to delete %s: %v\n", *obj.Key, err)
					} else {
						fmt.Printf("🗑️ DELETED: %s\n", *obj.Key)
						deletedCount++
					}
				}
			}
		}
	}

	// 4. FinOps Report / Summary
	fmt.Println("------------------------------------------------")

	// Calculate Savings
	sizeInGB := float64(totalSize) / 1024 / 1024 / 1024
	estimatedSavings := sizeInGB * pricePerGB

	if isReport {
		fmt.Println("📊 FINOPS COST REPORT")
		fmt.Printf("   • Stale Objects Found: %d\n", staleCount)
		fmt.Printf("   • Total Storage Reclaimable: %.4f GB\n", sizeInGB)
		fmt.Printf("   • Estimated Monthly Savings: $%.4f\n", estimatedSavings)
		fmt.Println("   (Based on S3 Standard pricing of ~$0.023/GB)")
		return
	}

	if isDryRun {
		fmt.Printf("✅ Dry run complete. Found %d stale objects (%.2f GB).\n", staleCount, sizeInGB)
		fmt.Println("   Run with --dry-run=false to execute cleanup.")
	} else {
		fmt.Printf("✅ Cleanup complete. Deleted %d objects.\n", deletedCount)
	}
}
