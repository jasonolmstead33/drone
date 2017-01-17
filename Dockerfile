# docker build --rm -t drone/drone .

FROM centurylink/ca-certs
EXPOSE 8000

#we do our release of the linux version
ADD release/linux/amd64/drone /drone

ENTRYPOINT ["/drone"]
CMD ["server"]
