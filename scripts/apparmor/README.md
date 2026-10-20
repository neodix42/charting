This is the unmodified `bwrap-userns-restrict` policy from AppArmor's
`apparmor-4.0` branch, compatible with Ubuntu 24.04's policy ABI.

- Upstream commit: `72229df83059480f4e9fb1488624201bdbd61755`.
- [Pinned policy source](https://gitlab.com/apparmor/apparmor/-/blob/72229df83059480f4e9fb1488624201bdbd61755/profiles/apparmor/profiles/extras/bwrap-userns-restrict).
- Policy SHA-256: `a964037f6cf0df1099f14226b037eaedde6237c86e715188e93eb460b30be859`.
- The upstream GPL-2.0 license is retained in `LICENSE.apparmor`.
- [Canonical's explanation and recommendation](https://discourse.ubuntu.com/t/understanding-apparmor-user-namespace-restriction/58007).

Only the disposable GitHub-hosted Ubuntu 24.04 smoke-test setup can load this
policy, and only after an unprivileged Bubblewrap namespace probe fails with a
permission denial and no conflicting policy exists. It grants namespace setup
to `/usr/bin/bwrap`; its stacked child policy explicitly denies capabilities.
It does not grant arbitrary unconfined applications namespace capabilities.

The helper loads the policy into the runner kernel with `--add --skip-cache`.
It does not replace policies, install files in `/etc`, write policy caches,
alter sysctls, disable AppArmor, or disable WebKit sandboxing. Compilation and
namespace probes run without privilege. If the runner cannot load or use the
policy, the release fails and retains the diagnostic report.
