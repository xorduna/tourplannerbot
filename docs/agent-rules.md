General guidelines:
- Create always long variable names, even if they are not used more than once. This makes it easier to understand the code and its purpose.
- Avoid using abbreviations or acronyms in variable names, unless they are widely known and accepted in the context of the code.
- Use descriptive names for functions and methods, that clearly indicate their purpose and functionality.
- Avoid using single-letter variable names, unless they are commonly used in the context of the code (e.g. i for loop index).
- Provide clear documentation on each function and method, including its parameters, return values, and any side effects it may have.
- User may speak in any language, but the code and documentation should always be in English.


Documentation:
- Update documentation in /docs folder
- Add the following metadata on top of each documentation file:
  - title: A clear and concise title that reflects the content of the documentation.
  - description: A brief summary of what the documentation covers and its purpose.
  - methods: A list of the main methods or functions that are described in the documentation, along with a brief description of each.
  - depends_on: List of files or modules that this file depends on.
  - used_by: List of files or modules that use this file.
- make sure that there is a bash script in the scripts folder that can be used to create a `code-index.md` file that contains all the metadata from the documentation files in the /docs folder, and that this script is run regularly to keep the code index up to date.

Memory:
- Use memory to store important information that may be needed later in the conversation, such as user preferences, previous interactions, or relevant context.
- Regularly review and update the memory to ensure it remains accurate and relevant.
- Use memory to provide personalized responses and recommendations based on the user's previous interactions and preferences.

Workflow:
- After each iteration, check `readme.md` and make sure is updated with the latest information about the project, specially folder structure and how to run the project.