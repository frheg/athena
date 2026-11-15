# speech: Piper text to speech (Python; Piper is GPL-3.0). docker build -t athena-speech speech
# Dependencies are pinned with hashes (requirements.txt, from requirements.in with uv pip compile --generate-hashes);
# voices are downloaded at build time and checked against voices.txt.
FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df AS build
WORKDIR /src
COPY requirements.txt .
RUN pip install --no-cache-dir --require-hashes --target /deps -r requirements.txt
COPY voices.txt fetch_voices.py ./
RUN python fetch_voices.py voices.txt /voices
COPY server.py test_server.py ./
RUN PYTHONPATH=/deps python -m unittest -q test_server

FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df
COPY --from=build /deps /deps
COPY --from=build /voices /voices
COPY server.py /app/server.py
ENV PYTHONPATH=/deps PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 VOICES_DIR=/voices
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["python", "/app/server.py", "health"]
ENTRYPOINT ["python", "/app/server.py"]
