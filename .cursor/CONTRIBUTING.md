# Contributing

Thanks for your interest in contributing!

This repository contains the Copilot SDK, a set of multi-language SDKs (Node/TypeScript, Python, Go, .NET, Java, and Rust) for building applications with the GitHub Copilot agent, maintained by the GitHub Copilot team.

Contributions to this project are [released](https://help.github.com/articles/github-terms-of-service/#6-contributions-under-repository-license) to the public under the [project's open source license](LICENSE).

Please note that this project is released with a [Contributor Code of Conduct](CODE_OF_CONDUCT.md). By participating in this project you agree to abide by its terms.

## Before You Submit a PR

**Please discuss any feature work with us before writing code.**

The team already has a committed product roadmap, and features must be maintained in sync across all supported languages. Pull requests that introduce features not previously aligned with the team are unlikely to be accepted, regardless of their quality or scope.

If you submit a PR, **be sure to link to an associated issue describing the bug or agreed feature**. No PRs without context :)

## What We're Looking For

We welcome:

- Bug fixes with clear reproduction steps
- Improvements to documentation
- Making the SDKs more idiomatic and nice to use for each supported language
- Bug reports and feature suggestions on [our issue tracker](https://github.com/github/copilot-sdk/issues) — especially for bugs with repro steps

We are generally **not** looking for:

- New features, capabilities, or UX changes that haven't been discussed and agreed with the team
- Refactors or architectural changes
- Integrations with external tools or services
- Additional documentation
- **SDKs for other languages** — if you want to create a Copilot SDK for another language, we'd love to hear from you and may offer to link to your SDK from our repo. However we do not plan to add further language-specific SDKs to this repo in the short term, since we need to retain our maintenance capacity for moving forwards quickly with the existing language set. For other languages, please consider running your own external project.

## Developing an SDK

Setup, build, and test instructions are maintained with each SDK:

- [Node.js/TypeScript](nodejs/README.md#development)
- [Python](python/README.md#development)
- [Go](go/README.md#development)
- [.NET](dotnet/README.md#development)
- [Rust](rust/README.md#development)
- [Java](java/README.md#development-setup)

## Submitting a Pull Request

1. Fork and clone the repository
1. Follow the development instructions for the SDK(s) you're modifying
1. Create a new branch: `git checkout -b my-branch-name`
1. Make your change, add tests, and run the documented checks
1. Push to your fork and [submit a pull request][pr]
1. Pat yourself on the back and wait for your pull request to be reviewed and merged.

Here are a few things you can do that will increase the likelihood of your pull request being accepted:

- Write tests.
- Keep your change as focused as possible. If there are multiple changes you would like to make that are not dependent upon each other, consider submitting them as separate pull requests.
- Write a [good commit message](http://tbaggery.com/2008/04/19/a-note-about-git-commit-messages.html).

## Resources

- [How to Contribute to Open Source](https://opensource.guide/how-to-contribute/)
- [Using Pull Requests](https://help.github.com/articles/about-pull-requests/)
- [GitHub Help](https://help.github.com)
