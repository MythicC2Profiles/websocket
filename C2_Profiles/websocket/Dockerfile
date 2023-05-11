FROM itsafeaturemythic/mythic_python_go:latest

WORKDIR /Mythic/

COPY [".", "."]

RUN make build

CMD make run