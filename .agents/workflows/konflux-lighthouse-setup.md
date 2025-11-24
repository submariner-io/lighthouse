#### Setting up Lighthouse Builds in Konflux on New Branch

**Prerequisites:**

- Configuration added in konflux-ci/build-definitions repo
- Existing Konflux-configured branch to copy files from (e.g., `release-0.21`)

**Placeholders:**

- `<target-branch>`: Your target branch (e.g., `release-0.22`)
- `<X-Y>`: Version with dashes (e.g., `0-22`)
- `<component>`: Component name (agent or coredns)

**Important:** Lighthouse uses gomod-only prefetch (NO RPM dependencies). Gomod paths differ per component (see Step 3).

**Repeat steps 1-6 for each component (agent, coredns):**

##### 1. Checkout Bot's PR Branch

Bot creates PRs on branches named `konflux-lighthouse-<component>-<X-Y>`.

```bash
git checkout konflux-lighthouse-<component>-<X-Y>
```

##### 2. Copy Tekton Configuration from Previous Release

```bash
TARGET_VERSION=$(echo "<target-branch>" | grep -oP '(?<=release-0\.)\d+$')
[ -z "$TARGET_VERSION" ] && { echo "ERROR: Invalid branch format"; exit 1; }
PREV_VERSION=$((TARGET_VERSION - 1))

for type in pull-request push; do
  git show "origin/release-0.${PREV_VERSION}:.tekton/lighthouse-<component>-0-${PREV_VERSION}-${type}.yaml" | \
    sed "s/0-${PREV_VERSION}/0-${TARGET_VERSION}/g; s/release-0\.${PREV_VERSION}/release-0.${TARGET_VERSION}/g" \
    > ".tekton/lighthouse-<component>-0-${TARGET_VERSION}-${type}.yaml"
done
```

**Note:** Extracts YAML from previous release and updates versions in one step. Avoids intermediate files and sed parameter insertion bugs.

##### 3. Update Gomod Prefetch Paths

**For lighthouse-agent:**

```bash
sed -i 's|\[{"type": "gomod", "path": "."}, {"type": "gomod", "path": "coredns"}, {"type": "gomod", "path": "tools"}]|[{"type": "gomod", "path": "."}, {"type": "gomod", "path": "tools"}]|' .tekton/lighthouse-agent-0-${TARGET_VERSION}-*.yaml
```

**For lighthouse-coredns:**

```bash
sed -i 's|\[{"type": "gomod", "path": "."}, {"type": "gomod", "path": "coredns"}, {"type": "gomod", "path": "tools"}]|[{"type": "gomod", "path": "./coredns"}]|' .tekton/lighthouse-coredns-0-${TARGET_VERSION}-*.yaml
```

**Note:** Agent uses root+tools modules, coredns uses only coredns module.

##### 4. Add Konflux Dockerfile

```bash
git checkout origin/release-0.${PREV_VERSION} -- package/Dockerfile.lighthouse-<component>.konflux
sed -i "s/release-0.${PREV_VERSION}/release-0.${TARGET_VERSION}/g" package/Dockerfile.lighthouse-<component>.konflux
```

**Note:** Lighthouse Dockerfiles have no CPE labels (only BASE_BRANCH update needed).

##### 5. Commit Changes

```bash
git add .tekton/lighthouse-<component>-0-${TARGET_VERSION}-*.yaml package/Dockerfile.lighthouse-<component>.konflux
git commit -s -m "Add Konflux config for lighthouse-<component>"
```

##### 6. Review and Push

```bash
git log origin/<target-branch>..HEAD
git status
git push origin konflux-lighthouse-<component>-<X-Y> --force-with-lease
```

Expected: 1 commit (bot's initial + your configuration), clean working tree.

##### 7. Verify All Component PRs

After completing both components:

```bash
for component in agent coredns; do
  gh pr list --head konflux-lighthouse-$component-<X-Y>
done
```

Expected: 2 PRs (one per component).

**Troubleshooting:**

- **Validation error "impossible ParamValues.Type"**: Step 3 sed pattern mismatch. Check release-0.21's prefetch-input value matches expected format. If different, update Step 3 sed pattern.
- **Commit message too long**: Use exactly "Add Konflux config for lighthouse-<component>" (40-43 chars).
- **RPM lockfiles**: Lighthouse has NO RPM dependencies. Do not create `.rpm-lockfiles` directory.
