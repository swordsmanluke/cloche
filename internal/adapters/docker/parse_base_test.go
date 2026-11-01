package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseBaseImage_IgnoresFromInsideRunContinuationsAndHeredocs(t *testing.T) {
	// manager's Dockerfile: a Python "from x import y" on a RUN continuation
	// line was taken as the base image, so the base digest label was never
	// recorded and a rebuilt cloche-agent base went unnoticed.
	content := []byte(`FROM cloche-agent:latest
RUN python3 -c "import sys; \
from bl_ext.mpfb.services import TargetService; \
print(1)"
RUN <<PY
from os import path
FROM not-an-image
PY
RUN echo done
`)
	assert.Equal(t, "cloche-agent:latest", parseBaseImage(content))
}

func TestParseBaseImage_MultiStageTakesTheLastFrom(t *testing.T) {
	content := []byte("FROM golang:1.22 AS build\nRUN go build \\\n  ./...\nFROM --platform=linux/amd64 alpine:3.20\n")
	assert.Equal(t, "alpine:3.20", parseBaseImage(content))
}

func TestParseBaseImage_ManagerDockerfileShape(t *testing.T) {
	content := []byte("FROM cloche-agent:latest\nUSER root\nRUN blender -b --python-expr \"import bpy; \\\nfrom bl_ext.blender_org.mpfb.services.targetservice import TargetService; \\\nprint(1)\"\n")
	assert.Equal(t, "cloche-agent:latest", parseBaseImage(content))
}
