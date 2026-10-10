# Contributing to Simple HTTP Relay

Thank you for your interest in contributing to **Simple HTTP Relay**!  
We welcome contributions ranging from bug fixes and documentation improvements to new features outlined in our roadmap.

---

## 1. Finding & Claiming Tasks (Anti-Collision Protocol)

To ensure smooth collaboration and prevent multiple contributors from working on the same task simultaneously:

1. **Browse Open Issues:**
   - Check the [Issues tab](https://github.com/simple-relay/simple-relay/issues) for tasks labeled [`good first issue`](https://github.com/simple-relay/simple-relay/labels/good%20first%20issue) or [`help wanted`](https://github.com/simple-relay/simple-relay/labels/help%20wanted).
   - Issues labeled `status:ready` are refined and available for pickup.
   - Do **NOT** start working on an issue labeled `status:in-progress` unless discussed with the current assignee.

2. **Claim an Issue:**
   - Leave a comment on the issue: *"I would like to work on this issue."*
   - A maintainer will assign you to the issue and apply the `status:in-progress` label.
   - Please work on **one issue at a time** to give others an opportunity to contribute.

3. **Inactivity Auto-Release Policy:**
   - If no draft PR or status update is provided within **14 days** of assignment, the issue will be unassigned and returned to `status:ready` for other contributors.

---

## 2. Git & Branching Workflow

1. **Fork & Branch:**
   - Fork the repository and create a feature branch off `main`:
     ```bash
     git checkout -b feat/<issue-id>-<short-description>
     # Example: git checkout -b feat/12-signature-verifier
     # For bug fixes: git checkout -b fix/<issue-id>-<short-description>
     ```

2. **Commit Guidelines:**
   - Write clear, imperative commit messages following [Conventional Commits](https://www.conventionalcommits.org/):
     - `feat: implement stripe signature verifier adapter`
     - `fix: handle context cancellation on slow upstream proxy`
     - `docs: update litestream deployment guide`

---

## 3. Local Development & Verification

### Prerequisites
- Go 1.22 or higher.
- Git.

### Verification Commands
Before submitting a Pull Request, ensure all tests pass and static compilation succeeds:

```bash
# 1. Run all unit and integration tests with data race detector
go test -race ./...

# 2. Verify pure static CGO-free compilation
CGO_ENABLED=0 go build -o /dev/null ./cmd/relay
```

---

## 4. Submitting a Pull Request

1. **Pull Request Template:**
   - Fill in all sections in the provided PR template.
   - Link the resolved issue using GitHub closing keywords:
     ```markdown
     Fixes #12
     ```
2. **Roadmap Updates:**
   - If your PR fulfills a task from [`docs/roadmap.md`](docs/roadmap.md), mark the corresponding checkbox `- [x]` as part of your pull request.
3. **Review Process:**
   - Maintainers will review your PR. Address any feedback with follow-up commits. Once approved and CI passes, your PR will be merged.
