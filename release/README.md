# release

This subtree owns the npm wrapper packages, per-platform binary carrier
packages, and the scripts used by GitHub Actions to build release artifacts and
publish them.

Source code for toolbox itself stays in the main repo packages. `release/`
contains only packaging and publish machinery.

`publish-npm.mjs` publishes platform packages first, then the meta package.
Use `--channel preview` to publish to the npm dist-tag `next`, so consumers
install preview builds as `@include-tools/toolbox@next`. Use `--channel stable`
to publish the same package names to the default stable channel.
