# Pull Request

## Description

Provide a brief description of the changes in this PR.

<!--
Please include:
- What this PR does
- Why these changes are needed
- Any relevant context or background
-->

## Type of Change

Please delete options that are not relevant:

- [ ] 🐛 Bug fix (non-breaking change which fixes an issue)
- [ ] ✨ New feature (non-breaking change which adds functionality)
- [ ] 💥 Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] 📚 Documentation update
- [ ] 🔧 Refactoring (no functional changes, no api changes)
- [ ] ⚡ Performance improvement
- [ ] 🧪 Test changes
- [ ] 🔒 Security improvement
- [ ] 🚀 CI/CD changes

## Related Issues

<!--
Link to related issues using:
- Fixes #123
- Closes #456
- Related to #789
-->

## Changes Made

<!-- Provide a detailed list of changes -->

-
-
-

## Testing

<!-- Describe the tests you ran to verify your changes -->

### Test Environment

- [ ] Local development
- [ ] Docker container
- [ ] Kubernetes cluster
- [ ] CI/CD pipeline

### Test Cases

- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Performance tests (if applicable)
- [ ] Security tests (if applicable)

### Manual Testing

<!-- Describe any manual testing performed -->

```bash
# Example commands used for testing
./kube-tunnel -port=8080
curl http://test-service.default.svc.cluster.local:8080/health
```

## Performance Impact

<!-- If this PR affects performance, please provide details -->

- [ ] No performance impact
- [ ] Performance improvement (provide metrics)
- [ ] Performance regression (justify why)
- [ ] Performance impact unknown

### Benchmarks

<!-- If applicable, provide before/after performance metrics -->

| Metric               | Before | After | Change |
| -------------------- | ------ | ----- | ------ |
| Cold start latency   |        |       |        |
| Warm request latency |        |       |        |
| Throughput           |        |       |        |
| Memory usage         |        |       |        |

## Security Considerations

- [ ] No security impact
- [ ] Security improvement
- [ ] Potential security implications (explain below)

<!-- If there are security implications, please describe them -->

## Breaking Changes

- [ ] This PR introduces breaking changes

<!-- If breaking changes, describe:
- What breaks
- Migration path for users
- Timeline for deprecation (if applicable)
-->

## Documentation

- [ ] Documentation updated (README, docs, comments)
- [ ] No documentation changes needed
- [ ] Documentation update will follow in separate PR

## Checklist

### Code Quality

- [ ] My code follows the project's style guidelines
- [ ] I have performed a self-review of my own code
- [ ] I have commented my code, particularly in hard-to-understand areas
- [ ] I have made corresponding changes to the documentation
- [ ] My changes generate no new warnings or errors
- [ ] I have removed any debugging code or console logs

### Testing

- [ ] I have added tests that prove my fix is effective or that my feature works
- [ ] New and existing unit tests pass locally with my changes
- [ ] I have tested the changes with different Kubernetes versions (if applicable)
- [ ] I have tested cross-platform compatibility (if applicable)

### Dependencies

- [ ] I have updated go.mod/go.sum if dependencies changed
- [ ] Any new dependencies are properly justified
- [ ] I have checked for security vulnerabilities in new dependencies

### Release Notes

- [ ] This change should be mentioned in release notes
- [ ] This change should be mentioned in upgrade guide
- [ ] This change requires communication to users

## Screenshots/Logs

<!-- If applicable, add screenshots or log outputs to help explain your changes -->

```
# Example log output or configuration
```

## Additional Notes

<!-- Any additional information that reviewers should know -->

## Reviewer Notes

<!-- For reviewers: what should they focus on? -->

### Focus Areas

- [ ] Code logic and correctness
- [ ] Performance implications
- [ ] Security considerations
- [ ] API design
- [ ] Error handling
- [ ] Test coverage
- [ ] Documentation completeness

### Testing Instructions

<!-- Specific instructions for reviewers to test the changes -->

1.
2.
3.

---

**Deployment Checklist** (for maintainers):

- [ ] Version bump required
- [ ] Changelog updated
- [ ] Migration guide updated (if breaking changes)
- [ ] Release notes prepared
- [ ] Backward compatibility verified
