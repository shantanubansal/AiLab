# The platform builder typically rewrites this Dockerfile via Kaniko +
# buildpacks. It's checked in so developers can build and run the template
# locally with `docker build` and `docker run` for parity.

FROM python:3.12-slim

WORKDIR /app

# Install deps first for layer cache.
COPY pyproject.toml ./
RUN pip install --no-cache-dir .

COPY agent.py ./

# Run as a non-root user — the platform enforces this too.
RUN useradd --uid 1000 --shell /bin/false agent
USER 1000

ENTRYPOINT ["python", "agent.py"]
