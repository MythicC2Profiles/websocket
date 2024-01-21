FROM itsafeaturemythic/mythic_python_go:latest as builder

WORKDIR /Mythic/

COPY [".", "."]

RUN make build

COPY [".docker/requirements.txt", "requirements.txt"]
RUN python3 -m pip wheel --wheel-dir /wheels -r requirements.txt

CMD make run


FROM python:3.11-slim-bookworm

COPY --from=builder /wheels /wheels
COPY --from=builder /mythic_websocket_server /mythic_websocket_server

RUN pip install --no-cache /wheels/*

WORKDIR /Mythic/

COPY [".", "."]

CMD ["python3", "main.py"]