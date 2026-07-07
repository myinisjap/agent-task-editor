FROM node:26-alpine AS builder
ARG INSECURE_SKIP_SSL_VERIFY=false
WORKDIR /app
COPY package*.json ./
RUN if [ "$INSECURE_SKIP_SSL_VERIFY" = "true" ]; then npm config set strict-ssl false; fi
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
