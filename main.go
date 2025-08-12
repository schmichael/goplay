package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func main() {
	fmt.Println("Go Container-aware GOMAXPROCS Debug Info")
	fmt.Println("Based on https://github.com/golang/go/issues/73193#user-content-proposal")
	fmt.Println("")
	fmt.Println("NumCPU:                 ", runtime.NumCPU())
	fmt.Println("$GOMAXPROCS:            ", os.Getenv("GOMAXPROCS"))
	fmt.Println("sched_getaffinity(2):   ", getaffin())
	fmt.Println("runtime.GOMAXPROCS(-1): ", runtime.GOMAXPROCS(-1))
	fmt.Print("cgroup limit:            ")

	eff, adj, err := cgroupLimit()
	if err != nil {
		fmt.Println("error retrieving cgroup limits:", err.Error())
	} else if eff == 0 && adj == 0 {
		fmt.Println("not in cgroup")
	} else {
		fmt.Printf("effective: %f -- adjusted: %f\n", eff, adj)
	}
}

func getaffin() string {
	cpuset := &unix.CPUSet{}
	err := unix.SchedGetaffinity(0, cpuset)
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("%v", *cpuset)
}

// gemini code

const (
	// cgroupV1 CPU controller path
	cgroupV1CPUPath = "/sys/fs/cgroup/cpu"
	// cgroupV2 root path
	cgroupV2Path = "/sys/fs/cgroup"
	// Unlimited quota value for cgroup v1
	cgroupV1UnlimitedQuota = -1
)

// main is the entry point of the program.
func cgroupLimit() (float64, float64, error) {
	effectiveLimit, err := getEffectiveCPULimit()
	if err != nil {
		return 0, 0, err
	}
	if effectiveLimit == 0 {
		return 0, 0, nil
	}

	// The adjusted CPU limit is the maximum of 2 and the ceiling of the effective limit.
	// This ensures a minimum value of 2 as per the requirements.
	adjustedLimit := math.Max(2.0, math.Ceil(effectiveLimit))

	return effectiveLimit, adjustedLimit, nil
}

// getEffectiveCPULimit determines the effective CPU limit by traversing the cgroup hierarchy.
// It returns the minimum CPU limit found in the hierarchy.
func getEffectiveCPULimit() (float64, error) {
	// Check if we are in a cgroup v2 environment first.
	// The existence of "cgroup.controllers" is a good indicator of a v2 hierarchy.
	if _, err := os.Stat(filepath.Join(cgroupV2Path, "cgroup.controllers")); err == nil {
		return getCgroupV2Limit()
	}

	// If not v2, assume v1.
	if _, err := os.Stat(cgroupV1CPUPath); err == nil {
		return getCgroupV1Limit()
	}

	return 0, nil
}

// getCgroupV1Limit handles the logic for cgroup v1.
func getCgroupV1Limit() (float64, error) {
	cgroupPath, err := getProcessCgroupPath("cpu")
	if err != nil {
		return 0, fmt.Errorf("failed to get cgroup v1 path: %w", err)
	}

	// The full path to the process's specific cgroup directory.
	fullPath := filepath.Join(cgroupV1CPUPath, cgroupPath)
	return walkHierarchy(fullPath, calculateV1CPUQuota, cgroupV1CPUPath)
}

// getCgroupV2Limit handles the logic for cgroup v2.
func getCgroupV2Limit() (float64, error) {
	cgroupPath, err := getProcessCgroupPath("") // For v2, the controller name is not prefixed in /proc/self/cgroup
	if err != nil {
		return 0, fmt.Errorf("failed to get cgroup v2 path: %w", err)
	}

	// The full path to the process's specific cgroup directory.
	fullPath := filepath.Join(cgroupV2Path, cgroupPath)
	return walkHierarchy(fullPath, calculateV2CPUQuota, cgroupV2Path)
}

// getProcessCgroupPath parses /proc/self/cgroup to find the path for a specific controller.
func getProcessCgroupPath(controller string) (string, error) {
	file, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ":")
		if len(parts) != 3 {
			continue
		}

		// For cgroup v1, the format is "id:controllers:path".
		// We look for the 'cpu' controller.
		// For cgroup v2, the format is "0::path".
		if (controller != "" && strings.Contains(parts[1], controller)) || (controller == "" && parts[1] == "") {
			return parts[2], nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", fmt.Errorf("cgroup path for controller '%s' not found in /proc/self/cgroup", controller)
}

// walkHierarchy traverses up the cgroup directory tree from a starting path
// up to a root path, calculating the CPU limit at each level.
// It returns the minimum limit found.
func walkHierarchy(startPath string, calcFunc func(string) (float64, error), rootPath string) (float64, error) {
	minLimit := math.Inf(1) // Initialize with positive infinity
	currentPath := startPath

	for {
		limit, err := calcFunc(currentPath)
		if err != nil {
			// It's possible for some levels not to have limits set, so we don't error out,
			// but we log it for debugging purposes.
			// fmt.Fprintf(os.Stderr, "Debug: could not calculate limit for %s: %v\n", currentPath, err)
		} else {
			// Update the minimum limit if the current one is smaller.
			minLimit = math.Min(minLimit, limit)
		}

		// Stop if we have reached the root of the cgroup filesystem.
		if currentPath == rootPath || currentPath == "/" {
			break
		}

		// Move to the parent directory.
		currentPath = filepath.Dir(currentPath)
	}

	if math.IsInf(minLimit, 1) {
		return 0, nil
	}

	return minLimit, nil
}

// calculateV1CPUQuota computes the CPU quota for a given cgroup v1 path.
func calculateV1CPUQuota(path string) (float64, error) {
	quotaFile := filepath.Join(path, "cpu.cfs_quota_us")
	periodFile := filepath.Join(path, "cpu.cfs_period_us")

	quota, err := readIntFromFile(quotaFile)
	if err != nil {
		return 0, err
	}

	// A quota of -1 in v1 means the cgroup has unlimited CPU time.
	if quota == cgroupV1UnlimitedQuota {
		return math.Inf(1), nil
	}

	period, err := readIntFromFile(periodFile)
	if err != nil {
		return 0, err
	}
	if period == 0 {
		return 0, fmt.Errorf("cpu.cfs_period_us is zero")
	}

	return float64(quota) / float64(period), nil
}

// calculateV2CPUQuota computes the CPU quota for a given cgroup v2 path.
func calculateV2CPUQuota(path string) (float64, error) {
	maxFile := filepath.Join(path, "cpu.max")

	content, err := os.ReadFile(maxFile)
	if err != nil {
		return 0, err
	}

	parts := strings.Fields(string(content))
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid format in cpu.max: %s", content)
	}

	// If quota is "max", it's unlimited.
	if parts[0] == "max" {
		return math.Inf(1), nil
	}

	quota, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}

	period, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}
	if period == 0 {
		return 0, fmt.Errorf("period in cpu.max is zero")
	}

	return float64(quota) / float64(period), nil
}

// readIntFromFile is a helper to read an integer from a file.
func readIntFromFile(filePath string) (int64, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return 0, err
	}
	val, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return 0, err
	}
	return val, nil
}
