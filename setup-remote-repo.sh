#!/bin/bash
set -e

# Google Cloud Setup Script for Cloud Shell

if [ -f .env ]; then
  export $(grep -v '^#' .env | xargs)
fi

echo "Authenticating GitHub..."
# Additional auth or setup steps can go here

if [ -n "$GITHUB_REPO" ]; then
  echo "Target repo: $GITHUB_REPO"
fi

if [ -n "$FIREBASE_PROJECT" ]; then
  echo "Configuring gcloud / Firebase project: $FIREBASE_PROJECT"
  # gcloud config set project "$FIREBASE_PROJECT"
fi

echo "Deploying to Firebase Hosting..."
# firebase deploy --only hosting --project "$FIREBASE_PROJECT"

echo "Setup completed successfully."
