package core

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/core/internal/packager"
)

// Size limit modes.
const (
	SizeLimitModeError = "error"
	SizeLimitModeWarn  = "warn"
)

// Lambda's limits: 50 MB for a direct-upload zip, 250 MB unzipped.
const (
	maxZippedBytes   int64 = 50 << 20
	maxUnzippedBytes int64 = 250 << 20
	largestPackages        = 5
)

type packageSize struct {
	name  string
	bytes int64
}

// packageSizes sums the on-disk sizes of entries per Package: the first path
// component, with a trailing ".libs" or "-<version>.dist-info" folded into
// the Package it belongs to. Largest first, ties by name.
func packageSizes(entries []packager.Entry, layout string) (sizes []packageSize, total int64, err error) {
	byName := map[string]int64{}
	for _, e := range entries {
		st, err := os.Stat(e.Path)
		if err != nil {
			return nil, 0, err
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(e.Name, layout), "/")
		if strings.HasSuffix(name, ".dist-info") {
			name, _, _ = strings.Cut(strings.TrimSuffix(name, ".dist-info"), "-")
		}
		name = strings.TrimSuffix(name, ".libs")
		byName[name] += st.Size()
		total += st.Size()
	}
	for n, b := range byName {
		sizes = append(sizes, packageSize{n, b})
	}
	sort.Slice(sizes, func(i, j int) bool {
		if sizes[i].bytes != sizes[j].bytes {
			return sizes[i].bytes > sizes[j].bytes
		}
		return sizes[i].name < sizes[j].name
	})
	return sizes, total, nil
}

func formatMB(b int64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }

func largestList(sizes []packageSize) string {
	if len(sizes) > largestPackages {
		sizes = sizes[:largestPackages]
	}
	parts := make([]string, len(sizes))
	for i, s := range sizes {
		parts[i] = fmt.Sprintf("%s (%s)", s.name, formatMB(s.bytes))
	}
	return strings.Join(parts, ", ")
}

func sizeWarning(code string, limit string, actual int64, sizes []packageSize) Warning {
	return Warning{
		Code: code,
		Message: fmt.Sprintf("Artifact is %s, above Lambda's %s limit. Largest Packages by unpacked size: %s.",
			formatMB(actual), limit, largestList(sizes)),
	}
}
