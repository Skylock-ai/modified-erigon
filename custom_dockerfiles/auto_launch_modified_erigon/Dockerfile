FROM modified_erigon_feb_17_2_38_1

# CMD ["/bin/sh"]
# RUN /bin/sh -c apk add --no-cache ca-certificates &&     mkdir -p /etc/erigon # buildkit
# COPY erigon /usr/local/bin/ # buildkit
# EXPOSE map[30303/tcp:{} 30303/udp:{} 42069/tcp:{} 42069/udp:{} 6060/tcp:{} 8080/tcp:{} 8545/tcp:{} 8546/tcp:{} 8551/tcp:{} 9090/tcp:{}]
ENTRYPOINT ["erigon"]
