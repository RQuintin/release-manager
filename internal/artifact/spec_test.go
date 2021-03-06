package artifact_test

import (
	"path"
	"testing"

	"github.com/lunarway/release-manager/internal/artifact"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	tt := []struct {
		name string
		path string
		spec artifact.Spec
		err  error
	}{
		{
			name: "existing and valid artifact",
			path: "valid_artifact.json",
			spec: artifact.Spec{
				ID: "valid",
			},
		},
		{
			name: "unknown artifact",
			path: "unknown_artifact.json",
			spec: artifact.Spec{},
			err:  artifact.ErrFileNotFound,
		},
		{
			name: "invalid artifact",
			path: "invalid_artifact.json",
			spec: artifact.Spec{},
			err:  artifact.ErrNotParsable,
		},
		{
			name: "unknown fields in artifact",
			path: "unknown_fields_artifact.json",
			spec: artifact.Spec{},
			err:  artifact.ErrUnknownFields,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := artifact.Get(path.Join("testdata", tc.path))
			if tc.err != nil {
				assert.EqualError(t, errors.Cause(err), tc.err.Error(), "output error not as expected")
			} else {
				assert.NoError(t, err, "no output error expected")
			}
			assert.Equal(t, tc.spec, spec, "spec not as expected")
		})
	}
}
