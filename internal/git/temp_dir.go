package git

import (
	"context"
	"io/ioutil"
	"os"

	"github.com/lunarway/release-manager/internal/log"
	"github.com/lunarway/release-manager/internal/tracing"
)

// TempDir returns a temporary directory with provided prefix.
// The first return argument is the path. The second is a close function to
// remove the path.
func TempDir(ctx context.Context, tracer tracing.Tracer, prefix string) (string, func(context.Context), error) {
	span, ctx := tracer.FromCtxf(ctx, "create temp dir for '%s'", prefix)
	defer span.Finish()
	path, err := ioutil.TempDir("", prefix)
	if err != nil {
		return "", func(context.Context) {}, err
	}
	return path, func(ctx context.Context) {
		span, ctx := tracer.FromCtxf(ctx, "clean temp dir for '%s'", prefix)
		defer span.Finish()
		err := os.RemoveAll(path)
		if err != nil {
			log.Errorf("Removing temporary directory failed: path '%s': %v", path, err)
		}
	}, nil
}
