# npm Build and Publish

The Artifactory plugin supports bounded npm operations through JFrog CLI:

- `command: install`
- `command: ci`
- `command: publish`

Use a Node-enabled `plugins/artifactory` image. The regular JVM/general
Artifactory images do not contain Node or npm.

```yaml
- step:
    type: Plugin
    name: npm ci with Artifactory
    identifier: npm_ci_with_artifactory
    spec:
      connectorRef: account.harnessImage
      image: plugins/artifactory:<node-enabled-windows-tag>
      settings:
        build_tool: npm
        command: ci
        url: https://example.jfrog.io
        access_token: <+secrets.getValue("jfrog_access_token")>
        repo_resolve: npm-remote
        repo_deploy: npm-local
        resolver_id: npm-resolver
        deployer_id: npm-deployer
        project_dir: frontend
        build_name: <+pipeline.identifier>
        build_number: <+pipeline.sequenceId>
        module: frontend
        publish_build_info: true
```

The `url` setting accepts either the JFrog platform URL or a URL containing
`/artifactory`; the plugin normalizes it for `jf config add` and repository
operations.

## Alternate npm versions

Node-enabled Windows images retain the npm version bundled with Node and the
approved alternates `5.6.0`, `6.4.1`, `6.11.3`, `6.14.4`, `6.14.7`, and
`6.14.8`.

Set `npm_version` only when an alternate is required:

```yaml
settings:
  build_tool: npm
  command: ci
  npm_version: 6.14.8
```

The plugin selects the alternate for its JFrog npm subprocess without changing
the image-wide default. Unknown or unavailable versions fail explicitly.

## Build information

`jf npm` receives `build_name`, `build_number`, `project`, and `module`.
When `publish_build_info: true` is set, the plugin publishes the collected
build information exactly once after the npm operation succeeds.

The standalone `command: publish-build-info` remains available for pipelines
that publish in a separate step.
