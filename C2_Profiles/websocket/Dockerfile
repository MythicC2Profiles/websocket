FROM itsafeaturemythic/mythic_base_image:0.0.2

RUN pip3 install mythic-container==0.2.0rc24

WORKDIR /Mythic/

COPY [".", "."]

RUN make build

CMD make run