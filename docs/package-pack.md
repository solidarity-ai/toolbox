# Packing a package

`toolbox package pack` compiles a development package and creates the two
artifacts consumed by package registries and Toolbox:

```sh
toolbox package pack [<package-dir>] --out <output-dir>
```

Both directories default to the current directory, so this is valid:

```sh
toolbox package pack
```

The command reads `toolbox.devpkg.json`, compiles TypeScript tool signatures
and JSDoc metadata, validates the result, resolves package resources, and
writes:

- `<package-name>.toolbox.pkg`, containing package sources and an internal
  compiled manifest
- `toolbox.pkg.json`, the external compiled manifest containing the archive's
  SHA-256

The paths of both files are printed after a successful pack. Packing is
intentionally disabled in unreleased development builds: the installed
executable must contain a valid Toolbox release version so
`packedByToolboxVersion` truthfully identifies the compiler.

## npm preview channel

During the initial rollout, run the released compiler directly from npm:

```sh
npx @include-tools/toolbox@next package pack . --out dist
```

CI may install it once instead:

```yaml
- name: Install Toolbox
  run: npm install --global @include-tools/toolbox@next
- name: Package
  run: toolbox package pack . --out dist
```

Upload both generated files as immutable release assets. The `next` selector is
temporary and will become `@include-tools/toolbox@latest` after the command
stabilizes.

`toolbox-pack` remains available as a compatibility wrapper; new automation
should use `toolbox package pack`.
