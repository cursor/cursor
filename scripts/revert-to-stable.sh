#!/bin/bash

TARGET_TAG="${1:-stable-point-v1.0}"

echo "========================================================="
echo "Revert to Stable Point Routine"
echo "Target Checkpoint: $TARGET_TAG"
echo "========================================================="

if git rev-parse "$TARGET_TAG" >/dev/null 2>&1; then
    echo "Stable checkpoint '$TARGET_TAG' found."
    echo "Creating backup branch..."
    git branch "backup-before-revert-$(date +%s)" || true

    echo "Reverting working tree to stable point '$TARGET_TAG'..."
    git checkout "$TARGET_TAG" -- .
    git reset "$TARGET_TAG"

    echo "Working tree successfully reset to stable checkpoint '$TARGET_TAG'."
else
    echo "Error: Checkpoint tag '$TARGET_TAG' not found!"
fi

echo "========================================================="
echo "Revert Execution Completed."
echo "========================================================="
