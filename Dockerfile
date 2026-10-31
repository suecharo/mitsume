FROM gcr.io/distroless/static-debian13
COPY mitsume /mitsume
ENTRYPOINT ["/mitsume"]
