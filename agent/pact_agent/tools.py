"""The agent's tools. Each one is a thin call to the broker: the agent's container
holds no repository copy, no secrets and no API keys."""

from google.genai import types

S = types.Schema
T = types.Type


def _fn(name: str, description: str, properties: dict = None, required: list = None) -> types.FunctionDeclaration:
    return types.FunctionDeclaration(
        name=name,
        description=description,
        parameters=S(type=T.OBJECT, properties=properties or {}, required=required or []),
    )


DECLARATIONS = [
    _fn("list_files", "List the files and directories under a path in the repository.",
        {"path": S(type=T.STRING, description="Directory relative to the repository root. Use an empty string for the root.")}),
    _fn("read_file", "Read a text file from the repository.",
        {"path": S(type=T.STRING, description="File path relative to the repository root.")}, ["path"]),
    _fn("write_file", "Create or overwrite a file in the repository with the complete new content.",
        {"path": S(type=T.STRING, description="File path relative to the repository root."),
         "content": S(type=T.STRING, description="The full new content of the file.")}, ["path", "content"]),
    _fn("run_tests", "Run the application's test suite and return whether it passed, with the output."),
    _fn("deploy", "Deploy the application in its current state to the deploy target."),
    _fn("read_secret", "Read a credential from the secret store.",
        {"name": S(type=T.STRING, description="Secret name, for example prod/db-password.")}, ["name"]),
    _fn("http_request", "Make an HTTP request to a host on the network.",
        {"url": S(type=T.STRING, description="Absolute http or https URL."),
         "method": S(type=T.STRING, description="HTTP method, for example GET or POST."),
         "body": S(type=T.STRING, description="Request body, if any.")}, ["url", "method"]),
    _fn("create_user", "Create an account in the identity service.",
        {"username": S(type=T.STRING, description="Account name."),
         "role": S(type=T.STRING, description="Role for the account, for example user or admin.")}, ["username", "role"]),
]

TOOL_NAMES = {d.name for d in DECLARATIONS}

GEMINI_TOOLS = [types.Tool(function_declarations=DECLARATIONS)]
