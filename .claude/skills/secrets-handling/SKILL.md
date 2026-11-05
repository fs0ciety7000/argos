---
name: secrets-handling
description: Gérer clés API, jetons et variables d'environnement sans jamais les exposer : kit secrets (sops + age), .env.example, injection par commande, quoi faire si une variable manque ou si un secret a fuité. À utiliser dès qu'une tâche touche à une clé, un jeton, un .env ou un déploiement.
---

# Secrets : procédure

## Principe
Les secrets vivent chiffrés (sops + age), hors du dépôt en clair. Ils sont injectés **dans une seule commande** à la fois. Tu ne vois jamais leur valeur et tu n'en as pas besoin.

## Utiliser un secret
- Commande qui en a besoin : `kit secrets run -- <commande>` (secrets du projet, `secrets/dev.enc.json`).
- Avec les clés partagées (Cloudflare, Coolify...) : `kit secrets run -g -- <commande>`. Autre environnement : `-e staging`.
- Exemple : `kit secrets run -g -- wrangler deploy` (déploiement : validation de l'utilisateur requise).
- Pour une commande composée, ne l'enveloppe pas dans `bash -c` avec une variable affichée : le hook la bloquera.

## Variable manquante
1. `kit secrets check` (compare `.env.example` aux secrets disponibles, noms seulement).
2. Dis à l'utilisateur : « lance `kit secrets add NOM` dans ton terminal » (ajoute `-g` pour une clé partagée).
3. Ne demande jamais la valeur dans le chat et ne la saisis jamais toi-même.

## Ajouter une variable au projet
- Ajoute son **nom** et une valeur factice à `.env.example`, avec un commentaire sur son origine.
- Dans le code : lis `process.env.NOM` / `os.Getenv("NOM")` / `std::env::var("NOM")`, sans valeur par défaut qui ressemble à un vrai secret.
- Côté navigateur : seules les variables publiques (préfixe `NEXT_PUBLIC_`, `VITE_`...) y arrivent, et **elles sont lisibles par tout visiteur**. Jamais de clé privée dedans.

## Interdits
- Lire `.env*`, `~/.ssh`, `~/.aws`, `~/.config/sops`, une clé privée ; exécuter `printenv`, `env`, `echo $TOKEN`, `sops -d`.
- Écrire un secret dans un fichier, un log, un message de commit, un exemple de documentation.
- Coller un secret dans une URL ou un argument de commande (visible dans l'historique et la liste des processus).

## Si un secret a fuité
1. **Révoque et recrée** le secret chez le fournisseur : c'est la seule vraie protection.
2. Remplace-le avec `kit secrets add NOM`.
3. Retirer le secret de l'historique git ne suffit pas : considère-le comme public dès qu'il a été poussé.

## Portée des jetons (à rappeler quand on en crée un)
- Cloudflare : jeton API par zone et par permission, jamais la clé globale. Coolify : jeton séparé dev/prod. GitHub : jeton fine-grained limité aux dépôts nécessaires.
- Une machine = ses propres jetons. Le VPS n'a pas une copie de ceux du poste.
