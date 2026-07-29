FROM postgres:15.18-alpine

RUN apk upgrade --no-cache

COPY ./init.sql /docker-entrypoint-initdb.d/

RUN rm /usr/local/bin/gosu

USER postgres

EXPOSE 5432

CMD ["postgres"]
