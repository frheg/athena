# speech: text to speech (Piper, Kokoro) and speech to text (whisper.cpp), in Python. docker build -t athena-speech speech
# Dependencies are pinned with hashes (requirements.txt, from requirements.in with uv pip compile --generate-hashes);
# voices and the speech recognition model are downloaded at build time and checked against voices.txt.

# whisper.cpp's server, from the release source (checked by sha256), built for any x86-64 CPU with AVX2 and without
# shared libraries, so the runtime image needs nothing extra
FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc AS whisper
ARG WHISPER_VERSION=1.9.5
ARG WHISPER_SHA256=ff1a9053feb509ff9d7729703355541ae9690073a6b1c40eb692c962e0dc1720
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl cmake make g++ && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN curl -fsSL -o w.tar.gz "https://github.com/ggml-org/whisper.cpp/archive/refs/tags/v${WHISPER_VERSION}.tar.gz" \
 && echo "${WHISPER_SHA256}  w.tar.gz" | sha256sum -c - \
 && tar xzf w.tar.gz --strip-components=1 && rm w.tar.gz
RUN cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_AVX=ON -DGGML_AVX2=ON \
      -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_OPENMP=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_SDL2=OFF \
      -DCMAKE_EXE_LINKER_FLAGS="-static-libstdc++ -static-libgcc" \
 && cmake --build build -j"$(nproc)" --target whisper-server \
 && cp build/bin/whisper-server /whisper-server

FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df AS build
WORKDIR /src
COPY requirements.txt .
RUN pip install --no-cache-dir --require-hashes --target /deps -r requirements.txt
COPY voices.txt fetch_voices.py ./
RUN python fetch_voices.py voices.txt /voices
COPY server.py test_server.py ./
RUN PYTHONPATH=/deps python -m unittest -q test_server

FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df
COPY --from=whisper /whisper-server /usr/local/bin/whisper-server
COPY --from=build /deps /deps
COPY --from=build /voices /voices
COPY server.py /app/server.py
ENV PYTHONPATH=/deps PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 VOICES_DIR=/voices
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["python", "/app/server.py", "health"]
ENTRYPOINT ["python", "/app/server.py"]
