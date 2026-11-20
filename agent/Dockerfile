# The untrusted agent. It ships no repository copy, no secrets and no API keys;
# compose puts it on agent_net alone, read-only, with every capability dropped.
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1

# curl is here only so the bypass test can show that a direct call has no route.
RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*

WORKDIR /agent
COPY requirements.txt .
RUN pip install -r requirements.txt && (python -m compileall -q /usr/local/lib/python3.12/site-packages >/dev/null 2>&1 || true)
COPY pact_agent ./pact_agent
RUN python -m compileall -q pact_agent

USER 10001
CMD ["python", "-m", "pact_agent.main"]
