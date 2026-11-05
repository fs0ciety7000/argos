---
name: deploy-coolify-cloudflare
description: Déployer et diagnostiquer des applications sur Coolify (VPS) et Cloudflare (Workers, DNS, proxy) : choix du mode de build, variables, volumes, domaines, certificats, santé, retour arrière. À utiliser pour tout déploiement ou panne d'hébergement.
---

# Déploiement Coolify + Cloudflare

Les interfaces et options évoluent : confirme dans la documentation officielle de la version utilisée (coolify.io/docs, developers.cloudflare.com) avant d'affirmer un réglage précis.

## Choisir où déployer
- **Cloudflare Workers/Pages** : sites et API sans état, Next.js via l'adaptateur Cloudflare (skill `nextjs-on-cloudflare`), stockage KV/R2/D1/Durable Objects.
- **Coolify (VPS)** : applications avec processus persistant, bases de données, PocketBase (volume), conteneurs Docker.
- Les deux peuvent coexister : front sur Cloudflare, API et données sur Coolify.

## Coolify
1. **Source** : dépôt Git connecté (application GitHub ou clé de déploiement). Un push sur la branche configurée déclenche le build.
2. **Mode de build** : Dockerfile (le plus prévisible), Nixpacks (rapide à démarrer) ou Docker Compose. Écris un `Dockerfile` explicite pour les projets sérieux ; image finale minimale, utilisateur non root.
3. **Variables** : définies dans la ressource Coolify, pas dans le dépôt. Distingue les variables de **build** de celles d'**exécution**. Compare les noms attendus (`.env.example`) aux noms présents.
4. **Stockage** : volume persistant pour tout ce qui doit survivre (PocketBase `pb_data`, uploads). Sans volume, les données disparaissent à chaque redéploiement.
5. **Santé** : configure un contrôle de santé (route `/health`) ; un conteneur « en cours » mais malade ne se remarque pas autrement.
6. **Diagnostic** : lis d'abord les logs de **build**, puis ceux d'exécution. Causes fréquentes : variable de build absente, port d'écoute différent de celui déclaré, mémoire insuffisante pendant le build, volume mal monté.
7. **Retour arrière** : redéploie le commit précédent ; pour la base, restaure la sauvegarde. Décris-le avant d'agir.

Le serveur MCP Coolify (`kit mcp setup`) permet de diagnostiquer et déployer en langage naturel ; ses actions destructrices demandent une confirmation.

## Cloudflare (DNS et proxy devant Coolify)
- Enregistrement DNS vers l'IP du VPS ; nuage orange = proxifié (protection, cache), gris = DNS seul.
- Mode SSL/TLS **Full (strict)** avec un certificat valide à l'origine. Si l'émission du certificat échoue derrière le proxy, passe temporairement l'enregistrement en DNS seul ou utilise un certificat d'origine Cloudflare. « Flexible » laisse le trafic en clair entre Cloudflare et le serveur : à éviter.
- Cloudflare Tunnel est une alternative qui évite d'exposer des ports du VPS.

## Cloudflare Workers (wrangler)
- Configuration dans `wrangler.jsonc` (ou `.toml`) ; bindings (KV, R2, D1, secrets) déclarés là, utilisés par nom dans le code.
- Développement local : `wrangler dev`. Environnements séparés (dev/staging/prod) plutôt qu'un seul.
- Secrets de Worker : `wrangler secret put NOM` (demande validation, la valeur est saisie par l'utilisateur). Jamais dans `wrangler.jsonc`.
- Commande avec jeton : `kit secrets run -g -- wrangler <commande>`. `deploy`, `delete` et `secret` exigent la validation de l'utilisateur.
- Skills à charger : `wrangler`, `workers-best-practices`.

## Avant tout déploiement en production
1. Tests, lint et build passent (résultats réels).
2. Variables et secrets attendus présents (`kit secrets check`, noms seulement).
3. Plan de retour arrière écrit.
4. Annonce l'action exacte en une ligne (environnement, commande, effet). Via `kit api` sur Coolify et Cloudflare, l'utilisateur a donné son accord permanent : pas d'attente. Hors `kit api` (wrangler deploy, suppression, rotation de secret), demande la validation explicite.
5. Après : statut, URL, logs récents, et rapport honnête de ce qui a été vérifié.

## Créer et déployer avec `kit api`
Les formats de requête dépendent de la version : vérifie la référence API de l'instance (Coolify : coolify.io/docs/api-reference ; Cloudflare : developers.cloudflare.com/api) avant d'envoyer un corps JSON, et lis la réponse (`HTTP <code>`).

1. **DNS (Cloudflare)** : `kit api cloudflare /zones --jq '.result | map({name,id})'`, puis vérifier que le nom est libre : `kit api cloudflare "/zones/<id>/dns_records?name=<fqdn>" --jq '.result | map({type,content,proxied})'`. Créer : `kit api cloudflare POST /zones/<id>/dns_records --json '{"type":"A","name":"<sous-domaine>","content":"<ip>","proxied":true}' --jq '.result.id'`. Le jeton doit avoir le droit DNS Edit sur la zone.
2. **Application (Coolify)** : créer la ressource (`POST /applications/...` selon la source : dépôt public, clé de déploiement, GitHub App, Dockerfile), puis `PATCH /applications/<uuid>` pour le domaine, le port, la santé.
3. **Variables** : `POST /applications/<uuid>/envs` avec `{"key":"NOM","value":"__SECRET:NOM__"}` ; les valeurs viennent des secrets chiffrés (`kit secrets add`), jamais du chat.
4. **Déploiement** : `kit api coolify TRIGGER "/deploy?uuid=<uuid>" --jq '.'`, puis surveiller `GET /applications/<uuid>` jusqu'à `running:healthy`, et tester l'URL.
5. **En cas d'échec** : lire les logs de build avant de réessayer ; ne jamais boucler sur un déploiement qui échoue.
`DELETE` est bloqué par les réglages : la suppression se fait dans l'interface ou après que l'utilisateur a levé la règle.
