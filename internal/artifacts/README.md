# Artifacts Package

This package manages file storage and path confinement enforcement for local card artifacts under the workspace.

## Local Artifact Policy

As implemented in commit `0578312`, local path confinement is fully enforced:
- Artifact files are stored under the workspace artifacts folder.
- Paths are validated and confined to prevent traversal or access outside the workspace root (`artifact_policy: local`).
- Field types of `type: artifact` maintain binary metadata in JSON (`{ uri, mime, size, sha256 }`), keeping heavy database states focused on metadata alone.

See also: [Spec Card-Type Examples](../../docs/spec/SPEC-CARDTYPE-EXAMPLES.md)
