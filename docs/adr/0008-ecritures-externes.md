# ADR-0008 — Deux catégories de sorties : les fonctionnelles et celles d'exploitation

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-012`, `E-033`, `E-044`, `E-066`, `E-093`, `E-105`, `E-107`, `E-123`
- **Références** : `CLAUDE.md` § Écritures externes ; `METHODE.md` § Exécution d'un plan, point 9

## Contexte

Le kit de pilotage impose qu'aucune écriture externe ne parte d'un poste de développement, et qu'une
intégration sans configuration bascule en mode « puits » : elle journalise et n'envoie pas.

Cette règle ne peut pas s'appliquer telle quelle ici. **Écrire sur une destination est la fonction du
produit**, pas un effet de bord : un koffr qui journalise au lieu d'écrire ne sauvegarde rien.
`E-123` exige par ailleurs des tests d'intégration réels, aller-retour compris. Appliquer la règle
sans discernement rendrait le produit intestable ; l'ignorer ferait partir des mails et des
poussées de parc depuis un poste de développement.

## Décision

Les sorties réseau sont classées en deux catégories, avec deux régimes distincts.

**Sorties fonctionnelles** — les destinations (`filesystem`, `s3`, `sftp`) et les moteurs de base.

- Elles écrivent réellement, y compris en développement et en test.
- Elles n'écrivent **jamais** vers un service tiers réel depuis un poste de développement ou la CI :
  les tests tournent contre des conteneurs éphémères (MinIO pour S3, un serveur SSH pour SFTP, les
  bases de `E-123`). Une liste blanche d'hôtes en configuration de test l'impose.
- Toutes passent par l'interface unique de destination (`E-066`) : `store.Store`.

**Sorties d'exploitation** — SMTP, webhook, liaison au serveur central, téléchargement d'outils.

- Elles passent par une **porte de sortie unique**, `internal/egress`, seul endroit du dépôt
  autorisé à ouvrir une connexion HTTP ou SMTP sortante hors `store/` et `engine/`.
- Hors production, la porte **journalise et n'envoie pas**. Les mails vont dans un collecteur local
  (MailHog ou équivalent). C'est le mode « puits » du kit, appliqué là où il a un sens.
- Une configuration absente met l'intégration en mode « puits », jamais en erreur, jamais vers une
  valeur réelle par défaut.

**Commun aux deux.**

- Aucun hôte, jeton, clé ou destinataire en dur dans le code ou en base : tout vient de la
  configuration validée au démarrage, avec les trois formes de `E-033`.
- Une règle de lint (`depguard`) interdit `net/http`, `net/smtp` et `os/exec` hors des paquets
  autorisés ; `forbidigo` interdit `os.Getenv` hors du paquet de configuration.
- Un test du lot 0 vérifie qu'aucune connexion sortante n'est ouverte avec la configuration de
  développement par défaut, hors destinations locales.

## Conséquences

- La distinction doit être visible dans l'arborescence, sinon elle s'érode : `store/` et `engine/`
  d'un côté, `egress/` de l'autre, et la règle de lint qui les sépare.
- Le téléchargement d'outils (`E-044`) passe par la porte de sortie : en développement, il ne
  télécharge rien et échoue avec un message clair, plutôt que d'aller chercher une archive réelle.
- La CI a besoin de conteneurs : elle ne tourne pas sur un exécuteur sans Docker. C'est une
  contrainte d'infrastructure à accepter dès le lot 0.
- **Ce qui rouvrirait la décision** : une destination qui ne serait pas simulable localement (aucune
  des trois du MVP n'est dans ce cas).

## Alternatives écartées

- **Appliquer le mode « puits » à tout** : rend le produit intestable et `E-123` inapplicable.
- **N'appliquer le mode « puits » à rien** : laisse partir mails, webhooks et poussées de parc depuis
  un poste de développement.
- **Un seul point de sortie pour tout, destinations comprises** : mélange une abstraction métier
  (la destination, avec sa reprise et son listage) et une commodité d'exploitation.
