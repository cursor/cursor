# Create a Container Action with the GitHub Actions Toolkit

[![GitHub Super-Linter](https://github.com/actions/container-toolkit-action/actions/workflows/linter.yml/badge.svg)](https://github.com/super-linter/super-linter)
[![Check dist/](https://github.com/actions/container-toolkit-action/actions/workflows/check-dist.yml/badge.svg)](https://github.com/actions/container-toolkit-action/actions/workflows/check-dist.yml)
![CI](https://github.com/actions/container-toolkit-action/actions/workflows/ci.yml/badge.svg)
[![CodeQL](https://github.com/actions/container-toolkit-action/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/actions/container-toolkit-action/actions/workflows/codeql-analysis.yml)
[![Coverage](./badges/coverage.svg)](./badges/coverage.svg)

Use this template to bootstrap the creation of a container action with the
GitHub Actions toolkit. :rocket:

This template includes compilation support, tests, a validation workflow,
publishing, and versioning guidance.

For more information on the GitHub Actions toolkit, see the
[`actions/toolkit` repository](https://github.com/actions/toolkit/tree/main/docs)

## Create Your Own Action

To create your own action, you can use this repository as a template! Just
follow the below instructions:

1. Click the **Use this template** button at the top of the repository
1. Select **Create a new repository**
1. Select an owner and name for your new repository
1. Click **Create repository**
1. Clone your new repository

> [!IMPORTANT]
>
> Make sure to remove or update the [`CODEOWNERS`](./CODEOWNERS) file! For
> details on how to use this file, see
> [About code owners](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners).

## Initial Setup

After you've cloned the repository to your local machine or codespace, you'll
need to perform some initial setup steps before you can develop your action.

> [!NOTE]
>
> You'll need to have a reasonably modern version of
> [Node.js](https://nodejs.org) and
> [Docker](https://www.docker.com/get-started/) handy (Node.js v20.x or later
> and Docker engine v20+ or later should work!). If you are using a version
> manager like [`nodenv`](https://github.com/nodenv/nodenv) or
> [`fnm`](https://github.com/Schniz/fnm), this template has a `.node-version`
> file at the root of the repository that can be used to automatically switch to
> the correct version when you `cd` into the repository. Additionally, this
> `.node-version` file is used by GitHub Actions in any `actions/setup-node`
> actions.

1. :hammer_and_wrench: Install the dependencies

   ```bash
   npm install
   ```

1. :building_construction: Package the TypeScript for distribution

   ```bash
   npm run bundle
   ```

1. :white_check_mark: Run the tests

   ```bash
   $ npm test

   PASS  ./index.test.js
     ✓ throws invalid number (3ms)
     ✓ wait 500 ms (504ms)
     ✓ test runs (95ms)

   ...
   ```

1. :hammer_and_wrench: Build the container

   Make sure to replace `actions/container-toolkit-action` with an appropriate
   label for your container.

   ```bash
   docker build -t actions/container-toolkit-action .
   ```

1. :white_check_mark: Test the container

   You can pass individual environment variables using the `--env` or `-e` flag.

   ```bash
   $ docker run --env INPUT_MILLISECONDS=2000 actions/container-toolkit-action
   ::debug::The event payload: {}
   16:19:19 GMT+0000 (Coordinated Universal Time)
   16:19:21 GMT+0000 (Coordinated Universal Time)

   ::set-output name=time::16:19:21 GMT+0000 (Coordinated Universal Time)
   ```

   Or you can pass a file with environment variables using `--env-file`.

   ```bash
   $ echo "INPUT_MILLISECONDS=2000" > ./.env.test

   $ docker run --env-file ./.env.test actions/container-toolkit-action
   ::debug::The event payload: {}
   16:19:19 GMT+0000 (Coordinated Universal Time)
   16:19:21 GMT+0000 (Coordinated Universal Time)

   ::set-output name=time::16:19:21 GMT+0000 (Coordinated Universal Time)
   ```

## Update the Action Metadata

The [`action.yml`](action.yml) file defines metadata about your action, such as
input(s) and output(s). For details about this file, see
[Metadata syntax for GitHub Actions](https://docs.github.com/en/actions/creating-actions/metadata-syntax-for-github-actions).

When you copy this repository, update `action.yml` with the name, description,
inputs, and outputs for your action.

## Update the Action Code

### Update the TypeScript Source

The [`src/`](./src/) directory is the heart of your action! This contains the
source code that will be run when your action is invoked. You can replace the
contents of this directory with your own code.

There are a few things to keep in mind when writing your action code:

- Most GitHub Actions toolkit and CI/CD operations are processed asynchronously.
  In `main.ts`, you will see that the action is run in an `async` function.

  ```javascript
  import * as core from '@actions/core'
  //...

  export async function run(): Promise<void> {
    try {
      //...
    } catch (error) {
      core.setFailed(error.message)
    }
  }
  ```

  For more information about the GitHub Actions toolkit, see the
  [documentation](https://github.com/actions/toolkit/blob/main/README.md).

### Update the Container

In this template, the container action runs a Node.js script,
`node /dist/index.js`, when the container is launched. Since you can choose any
base Docker image and language you like, you can change this to suite your
needs. There are a few main things to remember when writing code for container
actions:

- Inputs are accessed using argument identifiers or environment variables
  (depending on what you set in your `action.yml`). For example, the first input
  to this action, `milliseconds`, can be accessed in the Node.js script using
  the `process.env.INPUT_MILLISECONDS` environment variable or the
  `getInput('milliseconds')` function from the `@actions/core` library.

  ```bash
  // Use an action input
  const ms: number = parseInt(core.getInput('milliseconds'), 10)

  // Use an environment variable
  const ms: number = parseInt(process.env.INPUT_MILLISECONDS, 10)
  ```

- GitHub Actions supports a number of different workflow commands such as
  creating outputs, setting environment variables, and more. These are
  accomplished by writing to different `GITHUB_*` environment variables. For
  more information, see
  [Commands](https://github.com/actions/toolkit/blob/main/docs/commands.md).

  | Scenario             | Example                                             |
  | -------------------- | --------------------------------------------------- |
  | Set environment vars | `core.exportVariable('MY_VAR', 'my-value')`         |
  | Set outputs          | `core.setOutput('time', new Date().toTimeString())` |
  | Set secrets          | `core.setSecret('mySecret')`                        |
  | Prepend to `PATH`    | `core.addPath('/usr/local/bin')`                    |

## Publish the Action

So, what are you waiting for? Go ahead and start customizing your action!

1. Create a new branch

   ```bash
   git checkout -b releases/v1
   ```

1. Replace the contents of `src/` with your action code
1. Add tests to `__tests__/` for your source code
1. Format, test, and build the action

   ```bash
   npm run all
   ```

   > This step is important! It will run [`rollup`](https://rollupjs.org/) to
   > build the final JavaScript action code with all dependencies included. If
   > you do not run this step, your action will not work correctly when it is
   > used in a workflow.

1. Commit your changes

   ```bash
   git add .
   git commit -m "My first action is ready!"
   ```

1. Push them to your repository

   ```bash
   git push -u origin releases/v1
   ```

1. Create a pull request and get feedback on your action
1. Merge the pull request into the `main` branch

Your action is now published! :rocket:

For information about versioning your action, see
[Versioning](https://github.com/actions/toolkit/blob/main/docs/action-versioning.md)
in the GitHub Actions toolkit.

## Validate the Action

You can now validate the action by referencing it in a workflow file. For
example, [`ci.yml`](./.github/workflows/ci.yml) demonstrates how to reference an
action in the same repository.

```yaml
steps:
  - name: Checkout
    id: checkout
    uses: actions/checkout@v4

  - name: Test Local Action
    id: test-action
    uses: ./
    with:
      milliseconds: 1000

  - name: Print Output
    id: output
    run: echo "${{ steps.test-action.outputs.time }}"
```

For example workflow runs, check out the
[Actions tab](https://github.com/actions/container-toolkit-action/actions)!
:rocket:

## Usage

After testing, you can create version tag(s) that developers can use to
reference different stable versions of your action. For more information, see
[Versioning](https://github.com/actions/toolkit/blob/main/docs/action-versioning.md)
in the GitHub Actions toolkit.

To include the action in a workflow in another repository, you can use the
`uses` syntax with the `@` symbol to reference a specific branch, tag, or commit
hash.

```yaml
steps:
  - name: Checkout
    id: checkout
    uses: actions/checkout@v4

  - name: Test Local Action
    id: test-action
    uses: actions/container-toolkit-action@v1 # Commit with the `v1` tag
    with:
      milliseconds: 1000

  - name: Print Output
    id: output
    run: echo "${{ steps.test-action.outputs.time }}"
```

## Dependency License Management

This template includes a GitHub Actions workflow,
[`licensed.yml`](./.github/workflows/licensed.yml), that uses
[Licensed](https://github.com/licensee/licensed) to check for dependencies with
missing or non-compliant licenses. This workflow is initially disabled. To
enable the workflow, follow the below steps.

1. Open [`licensed.yml`](./.github/workflows/licensed.yml)
1. Uncomment the following lines:

   ```yaml
   # pull_request:
   #   branches:
   #     - main
   # push:
   #   branches:
   #     - main
   ```

1. Save and commit the changes

Once complete, this workflow will run any time a pull request is created or
changes pushed directly to `main`. If the workflow detects any dependencies with
missing or non-compliant licenses, it will fail the workflow and provide details
on the issue(s) found.

### Updating Licenses

Whenever you install or update dependencies, you can use the Licensed CLI to
update the licenses database. To install Licensed, see the project's
[Readme](https://github.com/licensee/licensed?tab=readme-ov-file#installation).

To update the cached licenses, run the following command:

```bash
licensed cache
```

To check the status of cached licenses, run the following command:

```bash
licensed status
```
