FROM itsafeaturemythic/python38_sanic_c2profile:0.0.6

RUN apt-get update && apt-get install -y --no-install-recommends \
    wget bash make && \
    rm -rf /var/lib/apt/lists/* && \
    wget https://go.dev/dl/go1.16.14.linux-amd64.tar.gz && \
    rm -rf /usr/local/go && tar -C /usr/local -xzf go1.16.14.linux-amd64.tar.gz

ENV PATH /usr/local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/go/bin