package brewrestart

import (
	"fmt"
	"sort"
)

// A full kernel buffer can be truncated. Grow and retry rather than treating
// it as complete: an incomplete list could conceal a second socket owner.
func collectPIDs(read func([]int32) (int, error)) ([]int, error) {
	const maxPIDs = 65536
	for capacity := 256; capacity <= maxPIDs; capacity *= 2 {
		buf := make([]int32, capacity)
		bytes, err := read(buf)
		if err != nil {
			return nil, fmt.Errorf("list user processes: %w", err)
		}
		if bytes < 0 || bytes > len(buf)*4 || bytes%4 != 0 {
			return nil, fmt.Errorf("invalid kernel PID buffer length")
		}
		if bytes == len(buf)*4 {
			continue
		}
		result := make([]int, 0, bytes/4)
		for _, pid := range buf[:bytes/4] {
			if pid > 0 {
				result = append(result, int(pid))
			}
		}
		sort.Ints(result)
		unique := result[:0]
		for _, pid := range result {
			if len(unique) == 0 || unique[len(unique)-1] != pid {
				unique = append(unique, pid)
			}
		}
		return unique, nil
	}
	return nil, fmt.Errorf("process list remained truncated at safety limit")
}
