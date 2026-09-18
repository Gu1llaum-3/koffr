# ADR-0009 — L'accès à la machine fait les droits ; le serveur central est le seul acteur distant

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-023`, `E-082`, `E-099`, `E-105`, `E-108`, `E-109`, `E-112`, `E-128`
- **Références** : hypothèse de `Q-10` ; `CLAUDE.md` § Definition of done, point 4 ; CDC § 6.1

## Contexte

Le gabarit du kit suppose un modèle de rôles `module:action` vérifié dans la couche métier. koffr n'a
pas d'utilisateurs : il a un opérateur qui a un accès `root` à la machine, et le § 3 met
l'authentification multi-utilisateur et le RBAC **hors périmètre** (`E-023`, `E-128`), avec sa
raison au § 10 : « l'interface locale est déjà protégée par l'accès à la machine ».

Il reste pourtant un contrôle d'accès réel, et il est au cœur du produit : celui du **serveur
central**, dont le § 6.1 énumère un pouvoir autorisé (proposer une expression cron) et cinq pouvoirs
refusés par conception. Et une inconnue : `E-099` (`F11.2`) exige « une authentification
configurée » dès que l'interface écoute hors de `127.0.0.1`, sans dire laquelle.

## Décision

Il n'y a pas de modèle de rôles dans koffr. Le contrôle d'accès porte sur **l'origine d'une
instruction**, pas sur l'identité d'un utilisateur.

- **Local** : quiconque accède à la machine et au fichier de configuration a tous les droits de
  koffr. C'est un constat, pas un renoncement : la clé privée reste hors de portée (ADR-0007), et
  c'est ce qui rend la position tenable.
- **Interface web** : dans le MVP, koffr **refuse de démarrer** si `--listen` désigne autre chose que
  la boucle locale. Le message renvoie vers un tunnel SSH ou un proxy inverse. C'est la lecture de
  `E-099` qui ne crée pas un demi-modèle d'authentification à côté d'un RBAC déclaré hors périmètre.
  Hypothèse en attendant la réponse à `Q-10`.
- **Serveur central** : une réponse reçue est un message **non fiable** jusqu'à validation. Elle est
  validée par une liste blanche stricte d'un seul champ — l'expression cron d'une base dont
  `allow_remote_schedule` vaut `true` (`E-108`) — puis contrôlée contre les garde-fous locaux
  (`min_interval`). Tout le reste est ignoré, et toute instruction hors champ est rejetée,
  journalisée et notifiée localement comme incident (`E-109`).
- **Cette validation vit dans le domaine**, pas dans la couche transport. Le paquet `uplink` décode
  et transporte ; c'est un cas d'usage du domaine qui décide ce qu'on accepte. C'est la lecture du
  point 4 de la Definition of done applicable ici : la décision d'autorisation n'est jamais dans
  l'adaptateur.
- **Restauration** : jamais déclenchable à distance, quelle que soit l'origine (`E-082`). La
  vérification n'est pas un contrôle de droits mais une propriété de conception — aucun chemin de
  code ne relie une réponse du serveur à une restauration, et un test le démontre.

## Conséquences

- Écart assumé au gabarit du kit : pas de rôles, pas de `module:action`, pas de table d'audit
  d'utilisateur. La Definition of done de `CLAUDE.md` est à lire, pour ce projet, comme « toute
  instruction distante est validée dans le domaine ».
- Refuser l'écoute hors boucle locale contrarie un opérateur qui voulait ouvrir son interface sur le
  réseau interne. C'est délibéré, et réversible par un ADR si `Q-10` répond autrement.
- Le scénario 10 du § 8 (serveur simulé envoyant un ordre de restauration) devient un test
  d'intégration obligatoire du lot qui active la liaison, et non une recette manuelle.
- **Ce qui rouvrirait la décision** : l'arrivée du multi-utilisateur, qui appartient au serveur
  central et donc à son propre CDC.

## Alternatives écartées

- **Mot de passe unique haché en configuration** : crée un mécanisme d'authentification à maintenir
  (rotation, verrouillage, session) pour un seul utilisateur, alors que le RBAC est hors périmètre.
- **Jeton statique en en-tête** : pratique pour un script, inconfortable au navigateur, et donne une
  fausse impression de protection sur un canal en clair.
- **Modèle `module:action` complet** : contredit `E-023` et `E-128`, et ajoute une couche que le CDC
  attribue explicitement au serveur central.
