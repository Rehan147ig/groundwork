FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json ./
COPY apps/console/package.json ./apps/console/package.json
RUN npm install

FROM node:22-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run build --workspace apps/console

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app/apps/console/.next ./apps/console/.next
COPY --from=build /app/apps/console/package.json ./apps/console/package.json
COPY --from=build /app/node_modules ./node_modules
WORKDIR /app/apps/console
EXPOSE 3000
CMD ["npm", "run", "start"]
