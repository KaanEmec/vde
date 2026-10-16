# Job: Signed releases and SBOM

Version: v1.0 · Epic: 04 Release engineering · Status: planned · Depends on: v0.2 08-03

## Goal

Operators can verify what they run.

## Tasks

- [ ] Sign images and binaries with `cosign` (keyless, GitHub OIDC); publish SBOMs
      (SPDX) with each release; provenance attestation.
- [ ] Reproducible builds (`-trimpath`, fixed `-ldflags`, pinned toolchain).
- [ ] Verification instructions in the install guide.

## Acceptance criteria

- `cosign verify` succeeds on the released image following the docs.
