package optics

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// systemMemoryBytes returns the total physical RAM in bytes, or 0 if it
// cannot be determined on the current platform.
func systemMemoryBytes() uint64 {
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
							return kb * 1024
						}
					}
				}
			}
		}
	}
	return 0
}

// memoryBudgetBytes returns the byte budget a single simulation is allowed to
// use. On Linux it derives from total RAM (75% on large machines, 50% on
// small ones); otherwise a conservative fixed fallback is used.
func memoryBudgetBytes() uint64 {
	total := systemMemoryBytes()
	if total == 0 {
		return 6 << 30 // 6 GiB conservative fallback
	}
	if total > 8<<30 {
		return total - total/4 // 75% of RAM
	}
	return total / 2 // 50% of RAM on <= 8 GiB machines
}

// estimatePeakBytes returns a conservative upper bound on the peak resident
// memory (bytes) of a simulation on an N x N grid. Ex and Ey are always
// allocated (2 slots/pixel); the asm_pad zero-padded 2N scratch adds 4
// slots/pixel, and output-plane cloning / temporaries account for the rest.
// The factor 8 complex128 slots (128 bytes) per pixel is deliberately generous
// so over-budget grids are rejected before any fatal out-of-memory.
func estimatePeakBytes(n int) uint64 {
	return uint64(n) * uint64(n) * 16 * 8
}

func humanBytes(b uint64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.1f TiB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// CheckGridMemory returns a descriptive error if a grid of the given side
// length would exceed the memory budget, so callers can fail cleanly instead
// of triggering a fatal runtime out-of-memory.
func CheckGridMemory(n int) error {
	peak := estimatePeakBytes(n)
	budget := memoryBudgetBytes()
	if peak > budget {
		return fmt.Errorf("网格 %d×%d 内存预计需要 %s，实际只有 %s，请减小网格边长", n, n, humanBytes(peak), humanBytes(budget))
	}
	return nil
}
