#!/bin/sh

while true; do
  # Acquire the lock. Block if another instance is running.
  /webhook-broker -command prune >> /files/export/app.log 2>&1
  ls -al /files/export/
  echo "Prune Ran"

  # Sleep for 5 seconds
  sleep 5
done

