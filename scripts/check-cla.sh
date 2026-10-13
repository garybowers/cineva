#!/usr/bin/env bash
# Checks that every commit in a change is covered by the Contributor Licence
# Agreement (CLA.md): its author is listed in CLA-SIGNATURES.txt, and the
# commit carries a Signed-off-by line from that author. The maintainer is
# exempt from both.
#
#   scripts/check-cla.sh [base]   base defaults to origin/main
#
# Plain git and bash, so it runs the same locally and under any CI.
set -euo pipefail

base="${1:-origin/main}"
root="$(git rev-parse --show-toplevel)"
signatures="$root/CLA-SIGNATURES.txt"

# The signatures as they stand at the head of the change, so a first-time
# contributor's own commit adding their line counts.
listed() { grep -v '^#' "$signatures" | grep -F -i "<$1>" || true; }

failed=0
while read -r sha; do
	email="$(git show -s --format='%ae' "$sha")"
	name="$(git show -s --format='%an' "$sha")"
	line="$(listed "$email")"
	subject="$(git show -s --format='%h %s' "$sha")"
	if [[ "$line" == *"| maintainer"* ]]; then
		continue
	fi
	if [[ -z "$line" ]]; then
		echo "not signed:    $subject — $name <$email> is not in CLA-SIGNATURES.txt"
		failed=1
		continue
	fi
	if ! git show -s --format='%(trailers:key=Signed-off-by,valueonly)' "$sha" | grep -F -i -q "<$email>"; then
		echo "no sign-off:   $subject — commit with 'git commit -s' (or 'git rebase --signoff $base')"
		failed=1
	fi
done < <(git rev-list --no-merges "$base..HEAD")

if (( failed )); then
	echo
	echo "Every commit must be covered by the Contributor Licence Agreement; see CLA.md."
	exit 1
fi
echo "CLA check passed."
