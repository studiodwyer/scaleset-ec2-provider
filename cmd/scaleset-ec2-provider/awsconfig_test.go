package main

import (
	"context"
	"testing"
)

// TestLoadAWSConfig_RegionApplied verifies that an explicitly-provided region
// (i.e. the value from the TOML config) is applied to the resulting AWS config,
// so AWS clients (Secrets Manager, EC2) honor it instead of requiring
// AWS_REGION/shared config. Region resolution via the default chain when the
// value is empty is environment-dependent and is not asserted here.
func TestLoadAWSConfig_RegionApplied(t *testing.T) {
	cases := []struct {
		name   string
		region string
	}{
		{"ap-southeast-2", "ap-southeast-2"},
		{"eu-west-1", "eu-west-1"},
		{"us-east-1", "us-east-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadAWSConfig(context.Background(), tc.region)
			if err != nil {
				t.Fatalf("loadAWSConfig: %v", err)
			}
			if cfg.Region != tc.region {
				t.Errorf("Region = %q, want %q", cfg.Region, tc.region)
			}
		})
	}
}
