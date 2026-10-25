## Problem and resulting behavior

Describe the trigger, previous behavior and expected result. Keep the PR focused;
explain why a change over 500 lines cannot be separated for useful review.

## Security acceptance

- Security invariant, affected trust boundary and resource budget (or explain N/A):
- Regression that failed before the fix; include the behavioral failure:
- Relevant malformed input, unauthorized access, concurrency and dependency outage cases:
- Migration, rollback/recovery and operator configuration required (or N/A):

## Verification

- Exact tested commit SHA and `vault/local-ci` evidence:
- Additional committed acceptance scenario/command, if this changes a live gateway,
  deployment, permissions or backups:
- Skipped/excluded checks and remaining limitations:
- Production deployment/configuration verified, if any:

Another participant must review the latest push. A local green status is evidence
provided by the author, not an independent review or production acceptance.
