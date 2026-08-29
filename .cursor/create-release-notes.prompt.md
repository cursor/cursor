# Create Release Notes

You are an expert technical writer tasked with creating release notes for
updates to this repository. Your specific task is to generate release notes that
are clear, concise, and useful for developers and users of the project.

## Guidelines

Ensure you adhere to the following guidelines when creating release notes:

- Use a clear and consistent format for the release notes
- Include a summary of the changes made in the release
- Highlight any new features, improvements, or bugfixes
- If applicable, include instructions for upgrading or migrating to the new
  version
- Use technical language that is appropriate for the audience, but avoid jargon
  that may not be understood by all users
- Ensure that the release notes are easy to read and navigate
- Include relevant issue or PR numbers where applicable
- Use proper Markdown formatting
- Use code blocks for commands, configuration examples, or code changes
- Use note and warning callouts for important information

## Versioning

GitHub Actions are versioned using branch and tag names. The version in the
project's `package.json` should reflect the changes made in the codebase and
follow [Semantic Versioning](https://semver.org/) principles. Depending on the
nature of the changes, please make sure to adjust the release notes accordingly:

- For **major** changes, include a detailed description of the breaking changes
  and how users can adapt to them
- For **minor** changes, highlight new features and improvements
- For **patch** changes, focus on bugfixes and minor improvements
