#### Diagnosing and Fixing Konflux CI Failures

**Purpose:** Investigate Konflux CI failures and optionally create fixes. Use this to understand why a build failed, then create a fix branch if you're ready to fix it.

##### 1. Prerequisites

**User must complete:**

```bash
gh auth login
oc login --web https://api.kflux-prd-rh02.0fk9.p1.openshiftapps.com:6443/
```

##### 2. Diagnose Failures

**User must provide:** PR number OR "most recent for branch X"

**Agent: Extract diagnostic info (PR query uncommented by default; for branch, swap comments):**

```bash
# For specific PR (substitute <pr-number>):
oc get snapshots -n submariner-tenant -l "pac.test.appstudio.openshift.io/pull-request=<pr-number>" --sort-by=.metadata.creationTimestamp -o json > /tmp/snapshots.json
# For branch (substitute <branch>, e.g., release-0.21 maps to submariner-0-21):
# oc get snapshots -n submariner-tenant -l "appstudio.openshift.io/application=submariner-<branch>" --sort-by=.metadata.creationTimestamp -o json > /tmp/snapshots.json

SNAPSHOT=$(jq -r '.items[-1].metadata.name' /tmp/snapshots.json)
BUILD_LOGS=$(oc get snapshot "$SNAPSHOT" -n submariner-tenant -o jsonpath='{.metadata.annotations.pac\.test\.appstudio\.openshift\.io/log-url}')
oc get snapshot "$SNAPSHOT" -n submariner-tenant -o jsonpath='{.metadata.annotations.test\.appstudio\.openshift\.io/status}' > /tmp/test-status.json
TEST_PLR=$(jq -r '.[].testPipelineRunName' /tmp/test-status.json)
TEST_DETAILS=$(jq -r '.[] | "\(.scenario): \(.status) - \(.details)"' /tmp/test-status.json)
OVERALL_STATUS=$(oc get snapshot "$SNAPSHOT" -n submariner-tenant -o jsonpath='{.status.conditions[?(@.type=="AppStudioTestSucceeded")].message}')
echo "$SNAPSHOT" > /tmp/snapshot-name.txt
echo "Snapshot: $SNAPSHOT"
echo "Build logs: $BUILD_LOGS"
echo "Test results: $TEST_DETAILS"
echo "Test logs: https://konflux-ui.apps.kflux-prd-rh02.0fk9.p1.openshiftapps.com/ns/submariner-tenant/pipelinerun/$TEST_PLR/logs"
echo "Overall status: $OVERALL_STATUS"
```

**Agent: Present bash output directly to user (output already formatted above).**

##### 3. Analyze Enterprise Contract Logs

**User:** Download log from test logs URL → `~/Downloads/`

**Agent: Verify log matches snapshot and extract report section:**

```bash
SNAPSHOT=$(cat /tmp/snapshot-name.txt)
APP=$(oc get snapshot "$SNAPSHOT" -n submariner-tenant -o jsonpath='{.spec.application}')
grep -q "$APP" ~/Downloads/submariner-enterprise-*.log || echo "WARNING: Log may not match snapshot"
LOG_FILE="$(ls -lt ~/Downloads/submariner-enterprise-*.log | head -1 | awk '{print $NF}')"
sed -n '/^[[:space:]]*Success: /,/^----- DEBUG OUTPUT -----/p' "$LOG_FILE" > /tmp/ec-report-full.txt
head -n -1 /tmp/ec-report-full.txt > /tmp/ec-report.txt
```

**Parse (don't read full file):**

```bash
# 1. Overall result
head -3 /tmp/ec-report.txt

# 2. Count by error type
grep -E "^[[:space:]]*(✕ \[Violation\]|› \[Warning\])" /tmp/ec-report.txt | sort | uniq -c | sort -rn

# 3. Identify which components are in this repo
ls .tekton/*-pull-request.yaml | sed 's|.tekton/\(.*\)-pull-request\.yaml|\1|'

# 4. Check repo components for violations
# For each component identified above, check violations:
grep -A 2 "Name: <component-name>\$" /tmp/ec-report.txt
# Example: grep -A 2 "Name: submariner-operator-0-21\$" /tmp/ec-report.txt

# 5. Tasks mentioned in violations
grep "Term:" /tmp/ec-report.txt | awk '{print $2}' | sort -u
```

**Agent:** Present bash command output to user.

##### 4. Create Fix Branch

**If you're ready to fix the issues:**

**User must provide:** Target branch (e.g., "release-0.21" or "devel")

```bash
# For numbered release (substitute 0.X and YYYY-MM-DD, e.g., release-0.21):
git checkout release-0.X
git merge --ff-only origin/release-0.X
git checkout -b fix-0.X-konflux-YYYY-MM-DD

# For devel (substitute YYYY-MM-DD):
# git checkout devel
# git merge --ff-only origin/devel
# git checkout -b fix-devel-konflux-YYYY-MM-DD
```

Add -v2, -v3 etc if branch exists; don't delete old versions.

##### 5. Apply Fixes

**Note:** Running `bump-task-refs` alone (SHA updates only) may not fix violations if task versions are outdated. Always check and upgrade versions first.

For each failing task identified in step 3, check current and latest versions:

```bash
# Example: check buildah-remote-oci-ta
grep "task-buildah-remote-oci-ta" .tekton/*.yaml | head -1
# Shows current version, e.g.: task-buildah-remote-oci-ta:0.4@sha256:...

TASK="buildah-remote-oci-ta"
curl -sL "https://quay.io/api/v1/repository/konflux-ci/tekton-catalog/task-${TASK}/tag/" | jq -r '.tags[].name' | grep -E "^[0-9]+\.[0-9]+$" | sort -Vu | tail -1
# Shows latest version, e.g.: 0.6
```

Repeat for all failing tasks to identify which need version upgrades.

For each task where current < latest (e.g., current 0.4 or 0.5, latest 0.6), edit the matching `.tekton/*.yaml` files to upgrade version numbers.

Update SHAs (always run, even if no version edits needed) and commit:

```bash
PATCHER_SHA="b001763bb1cd0286a894cfb570fe12dd7f4504bd"
EXPECTED_SHA256="080ad5d7cf7d0cee732a774b7e4dda0e2ccf26b58e08a8516a3b812bc73beb53"

SCRIPT=$(curl -sL "https://raw.githubusercontent.com/simonbaird/konflux-pipeline-patcher/${PATCHER_SHA}/pipeline-patcher")
ACTUAL_SHA256=$(echo "$SCRIPT" | sha256sum | cut -d' ' -f1)

if [[ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]]; then
  echo "ERROR: Script checksum mismatch!"
  exit 1
fi

echo "$SCRIPT" | bash -s bump-task-refs
git add .tekton/*.yaml
git commit -s -m "Update Tekton task refs to latest versions"
```

**For other errors:** Consult team or see relevant documentation.

##### 6. Create Pull Request (Optional)

**Agent: Generate PR commands and provide them directly to user in response text.**

Extract variables needed for PR command:

```bash
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
BASE_BRANCH=$(echo $CURRENT_BRANCH | sed 's/fix-\([0-9.]*\)-.*/release-\1/; s/fix-devel-.*/devel/')
COMMIT_COUNT=$(git log ${BASE_BRANCH}..HEAD --oneline | wc -l)
FORK_REMOTE=$(git remote -v | awk '!/submariner-io/ && /\(push\)/ { print $1; exit }')
FORK_USER=$(git remote get-url ${FORK_REMOTE} 2>/dev/null | awk -F '[:/]' '{print $2}')

# If single commit, use commit message as PR body
if [ "$COMMIT_COUNT" -eq 1 ]; then
  PR_BODY=$(git log -1 --format=%B | head -1)
else
  PR_BODY="See commit messages for details."
fi
```

Substitute variables and provide in response (not bash output):

```bash
git push <FORK_REMOTE> <CURRENT_BRANCH> && \
gh pr create \
  --title "Fix Konflux CI failures in <BASE_BRANCH>" \
  --body "<PR_BODY>" \
  --base "<BASE_BRANCH>" \
  --head "<FORK_USER>:<CURRENT_BRANCH>" \
  --assignee "@me"
```

User reviews commits, then copies and runs if desired.

##### 7. Verify Fix (After PR Created)

**User:** Confirm PR has been created.

**Agent:** Check build status using current branch. If not complete, wait and recheck as needed.

```bash
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
BASE_BRANCH=$(echo $CURRENT_BRANCH | sed 's/fix-\([0-9.]*\)-.*/release-\1/; s/fix-devel-.*/devel/')
PR_NUMBER=$(gh pr list --head "$CURRENT_BRANCH" --base "$BASE_BRANCH" --json number --jq '.[0].number')
echo "Checking PR #$PR_NUMBER"
oc get snapshots -n submariner-tenant -l "pac.test.appstudio.openshift.io/pull-request=${PR_NUMBER}" --sort-by=.metadata.creationTimestamp -o json > /tmp/snapshots.json
SNAPSHOT=$(jq -r '.items[-1].metadata.name' /tmp/snapshots.json)

if [ "$SNAPSHOT" = "null" ] || [ -z "$SNAPSHOT" ]; then
  echo "No snapshot found yet. Build may not have started. Wait a few minutes and recheck."
else
  OVERALL_STATUS=$(oc get snapshot "$SNAPSHOT" -n submariner-tenant -o jsonpath='{.status.conditions[?(@.type=="AppStudioTestSucceeded")].status}' 2>/dev/null)
  echo "Snapshot: $SNAPSHOT"
  echo "Status: $OVERALL_STATUS"

  if [ "$OVERALL_STATUS" = "True" ] || [ "$OVERALL_STATUS" = "False" ]; then
    echo "Build complete."

    # Save snapshot name for step 3 compatibility
    echo "$SNAPSHOT" > /tmp/snapshot-name.txt

    # Identify which component triggered this build
    BUILD_LOGS=$(oc get snapshot "$SNAPSHOT" -n submariner-tenant -o jsonpath='{.metadata.annotations.pac\.test\.appstudio\.openshift\.io/log-url}')
    COMPONENT=$(echo "$BUILD_LOGS" | grep -oP '(?<=/pipelinerun/)[^/]+-on-(pull-request|push)' | sed 's/-on-.*//')
    echo "Component: $COMPONENT"

    # Get test logs URL
    oc get snapshot "$SNAPSHOT" -n submariner-tenant -o jsonpath='{.metadata.annotations.test\.appstudio\.openshift\.io/status}' > /tmp/test-status.json
    TEST_PLR=$(jq -r '.[].testPipelineRunName' /tmp/test-status.json)
    echo "Test logs: https://konflux-ui.apps.kflux-prd-rh02.0fk9.p1.openshiftapps.com/ns/submariner-tenant/pipelinerun/$TEST_PLR/logs"
  else
    echo "Build still running. Wait and recheck."
  fi
fi
```

**Note:** Status check shows overall EC pass/fail only. Component-specific violations are visible in the EC report (steps below).

**Agent:** Once complete, provide test logs URL to user.

**User:** Download log from test logs URL → `~/Downloads/`

**Agent:** Follow step 3 to analyze results. Check the component identified above for violations.

**Note:** Each component builds independently and triggers its own EC validation. The EC report shows the newly-built component plus all other components from their most recent builds (not necessarily from the same PR).

Expected: The identified component shows 0 violations. Other repo components may show violations if they haven't rebuilt yet from this PR. Overall build may still fail due to other repo components (outside scope of this PR).

If PR touches multiple components in the repo, repeat verification for each component's snapshot.
