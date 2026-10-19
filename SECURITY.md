# Security policy

## Supported versions

`vde` is at v0.x. Only the latest minor release (v0.N) receives security fixes, as a
patch release. Fixes are not backported to earlier minors; upgrade to the latest. Once v1.0
exists, this section will name the supported 1.x lines.

## Reporting a vulnerability

Report privately through GitHub security advisories for
[github.com/kaanemec/vde](https://github.com/kaanemec/vde): open the repository's
Security tab and choose "Report a vulnerability". Do not open a public issue or pull
request for a vulnerability.

Include the version (`vde version`), a description of the impact, and steps or a minimal
configuration to reproduce. Do not send real documents or credentials; use synthetic ones.

You can expect an acknowledgment within 7 days.

## Disclosure timeline

- Day 0: report received. Day 7 at the latest: acknowledged.
- The maintainers confirm or decline the report and agree a fix schedule with you.
- A fix is released and the advisory published as soon as it is ready, and in any case
  within 90 days of the report. Reporters are credited unless they ask not to be.
- If a fix needs longer than 90 days, we agree an extension with you before the deadline;
  absent agreement, you may disclose after 90 days.
- Vulnerabilities that are actively exploited may be disclosed sooner, with mitigations.

## Scope

In scope: the `vde` binary, its container image and the published documentation. The
service's security model, controls and known limits are described in
[docs/security.md](docs/security.md); behavior listed there as a non-control is not a
vulnerability, though improvements are welcome as ordinary issues. Out of scope: flaws in
model providers or in third-party reverse proxies, and findings that need `server.insecure_http`
enabled on an untrusted network, which are documented as unsafe.
