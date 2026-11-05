# CLAUDE.md

> Lu automatiquement à chaque session. Court exprès. Les règles générales (langue, secrets, routage des skills et des agents) sont dans `~/.claude/CLAUDE.md`.

@AGENTS.md

## Projet courant

- **Type** : `backend-go` + `web-frontend` (Svelte) + `design-ui` + `deploy` (voir `~/.claude/skills-registry.md`)
- **Stack** : Go 1.24 (`net/http`, `x/crypto/ssh`, SQLite pur Go `modernc.org/sqlite`) + Svelte 5 / Vite (SPA embarquée dans le binaire via `embed.FS`), WebSocket pour le temps réel, graphes uPlot
- **Commandes** : `make install`, `make dev-api` + `make dev-web`, `make test`, `make lint`, `make build`
- **Déploiement** : Coolify (Dockerfile, image Alpine non root), volume `/data`, sous-domaine Cloudflare

## Documents du projet

- `docs/brief.md` : objectif, périmètre, contraintes.
- `DESIGN.md` : direction visuelle (couleurs, typographie, composants). À respecter pour toute interface.
- `DEPLOY.md` : cible Coolify ou Cloudflare, sous-domaine, DNS, variables (noms), retour arrière.

## Spécifique à ce projet

- Skills : `golang-how-to` pour tout code Go ; `svelte-core-bestpractices` + `svelte-code-writer` (lancer `svelte-autofixer`) pour tout `.svelte`. `frontend-dev` est orienté React : pour Svelte, travaille toi-même ou précise Svelte 5 dans la consigne.
- Accès aux hôtes : seedboxes (Whatbox, HbD) en **lecture seule** ; seul Netcup accepte des actions Docker. Aucune commande shell arbitraire depuis l'UI : uniquement des actions nommées, validées côté serveur.
- Clés SSH et secrets : uniquement côté serveur (volume `/data` ou variables), jamais envoyés au frontend ni écrits dans les logs.
- Toute route hors `/health` exige une session authentifiée (V1 : compte unique, argon2id + TOTP).
- `web/dist/` est un artefact de build (seul `.gitkeep` est versionné) : lance `npm run build` avant `go build`.
- Variables d'environnement : noms et rôle dans `.env.example` ; valeurs chiffrées dans `secrets/` (`kit secrets`).
