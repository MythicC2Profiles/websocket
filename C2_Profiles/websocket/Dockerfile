FROM itsafeaturemythic/mythic_python_go:latest as builder

COPY [".docker/requirements.txt", "requirements.txt"]
RUN python3 -m pip wheel --wheel-dir /wheels -r requirements.txt

WORKDIR /Mythic/

COPY [".", "."]

RUN make build

FROM python:3.11-slim-bookworm

COPY --from=builder /wheels /wheels
COPY --from=builder /mythic_websocket_server /mythic_websocket_server

RUN pip install --no-cache /wheels/*
RUN apt-get update && apt-get install --no-install-recommends  make -y

WORKDIR /Mythic/

COPY [".", "."]

CMD make run