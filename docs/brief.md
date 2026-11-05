# Argos — brief

> Argos Panoptes : le géant aux cent yeux qui ne dort jamais.

## Objectif

Un hub unique pour surveiller et piloter l'infrastructure auto-hébergée (VPS Netcup, seedboxes Whatbox et HbD) : santé des machines, conteneurs Docker, logs et stockage (30+ TB), sans passer d'un outil à l'autre (Coolify, Plex/Jellyfin, SSH).

## Public

Usage personnel : un seul utilisateur, une seule instance.

## Fonctionnalités indispensables (V1)

1. **Inventaire des hôtes** : connexion SSH par clé, sans agent installé sur les machines.
2. **Métriques temps réel** : CPU, RAM, réseau, poussés au frontend par WebSocket, avec graphes.
3. **Conteneurs Docker** (VPS Netcup uniquement) : état, redémarrage manuel, redémarrage automatique des conteneurs défaillants.
4. **Logs en flux continu** : conteneurs Docker et journald ; fichiers de logs utilisateur sur les seedboxes.
5. **Stockage** : remplissage des disques et quotas par hôte, seuils visuels avant saturation.

### Périmètre par type d'hôte

| Hôte | Accès | V1 |
|---|---|---|
| Netcup (VPS) | SSH + Docker | lecture + actions (restart conteneurs) |
| Whatbox, HbD (seedbox partagée) | SSH sans root, sans Docker | lecture seule : disque, quota, processus, logs |

## Historique

Métriques conservées dans un **SQLite embarqué** (rétention de quelques jours, par défaut 7 jours) pour tracer des courbes au-delà du temps réel.

## Hors périmètre (V1)

- Notifications externes (Discord, Telegram, ntfy) : le dashboard suffit.
- Remplacer Coolify : on lit son état, on ne déploie pas.
- Gérer Plex/Jellyfin : affichage de l'état seulement.
- Multi-utilisateur, rôles.
- Actions sur les seedboxes (lecture seule).

## Contraintes

- Backend **Go** (SSH, Docker, WebSocket), frontend **Svelte 5**.
- UI « monitoring industriel / cyberpunk épuré » : dark mode profond, typographie monospace, beaucoup de dataviz.
- Hébergement : VPS Netcup via Coolify, sous-domaine Cloudflare.
- Sécurité : l'outil détient des clés SSH et peut relancer des services → authentification forte obligatoire, clés jamais exposées au frontend, aucune commande shell arbitraire depuis l'UI.
