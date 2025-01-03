#!/bin/bash

# Create a temporary file
tempfile="testDockerfile"

# Use sed to remove the last line and write to the temp file
sed '$d' Dockerfile > "$tempfile"

# Read all but the last line of Dockerfile into a variable.
dockerfile_content=$(cat "$tempfile")

# Remove the temporary file
rm "$tempfile"

# Escape ampersands and newlines in the dockerfile_content variable.
dockerfile_content=$(printf '%s\n' "$dockerfile_content" | sed -e ':a' -e '$!N;s/\n/\\n/g;ta' -e 's/&/\\&/g')

# Use sed with a different delimiter and escaped ampersands for replacement.
sed "s|{{include Dockerfile}}|${dockerfile_content}|g" ./integration-test/prune-integration-test.Dockerfile.template > ./integration-test/prune-integration-test.Dockerfile
