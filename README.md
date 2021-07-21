# devbase

This repo contains files needed for a Outreach repository. This is currently used as a library by bootstrap for shell scripts and other configuration.

The goal of this is to have one place for all scripts / etc needed to run outreach services and that this gets pulled in as needed.

## How to use a custom build of `devbase`

### Bootstrap 

To test a custom build of `devbase` with bootstrap, simply modify `bootstrap.lock` to point to your branch / version. 

**Example**: To use `jaredallard/feat/my-cool-feature` instead of `v1.8.0`, you'd update `versions.devbase` to the former. Full example below:

```yaml
# THIS IS AN AUTOGENERATED FILE. DO NOT EDIT THIS FILE DIRECTLY.
# vim: set syntax=yaml:
version: v7.4.2
generated: 2021-07-15T18:33:49Z
versions:
  devbase: jaredallard/feat/my-cool-feature
```

CI will now use that branch, to use it locally re-run any `make` command. **Note**: This will not automatically update locally when the remote branch is changed, in order to do that you will need to `rm -rf .bootstrap` and re-run a `make` command.
