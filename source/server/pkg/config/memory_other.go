//go:build !darwin && !linux

package config

func detectPhysicalMemoryBytes() uint64 { return 0 }
