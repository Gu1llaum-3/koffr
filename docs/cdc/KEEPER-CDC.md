# Keeper — Cahier des charges MVP

> **Version** 0.2 · brouillon soumis à revue · 18 septembre 2026
> **Nature** Spécification d'un **nouveau projet**, à créer de zéro. Ce document ne décrit aucune modification d'un dépôt existant.
> **Langage** Go 1.23+ · **Moteurs MVP** PostgreSQL, MySQL, MariaDB · **Destinations** disque, S3, SFTP

Un agent de sauvegarde de bases de données autonome, distribué en un seul binaire, qui n'a besoin ni de Docker, ni d'un serveur central, ni d'aucun service tiers pour faire son travail — et qui reste sûr même quand le serveur central tombe.

---

## Instructions pour l'agent d'implémentation

Si tu lis ce document pour écrire du code, lis d'abord cette section.

1. **C'est un projet neuf.** Rien n'existe. Le premier geste est de créer le module Go, pas de chercher du code à modifier.
2. **Commence par le lot L0** (§9). Il ne produit aucune sauvegarde, et c'est voulu : il valide la thèse technique du produit (la résolution des outils) avant qu'on ait investi dans le reste.
3. **Le §2 et le §6 ne sont pas négociables.** Les six principes directeurs et le modèle de menace sont le contrat du produit. Si une décision d'implémentation les contredit, c'est la décision qui change, pas le contrat. En cas de doute, demande — ne tranche pas seul.
4. **Les identifiants d'exigence sont stables** (`F2.3`, `N7`, `L1`…). Cite-les dans les commits, les tests et les messages de PR.
5. **Tout ce qui est marqué `Obligatoire` conditionne la livraison du MVP.** `Souhaitable` est à faire si le calendrier le permet, et à signaler explicitement si écarté.
6. **Ne réimplémente jamais `pg_dump` ni `mysqldump`.** C'est un non-objectif explicite (§1.3), pas une contrainte de temps.
7. **Si une exigence te paraît fausse ou incohérente, dis-le** plutôt que de l'appliquer mécaniquement. Ce document a été écrit avant la première ligne de code ; il contient sûrement des erreurs.

---

## Table des matières

1. [Contexte et objectifs](#1-contexte-et-objectifs)
2. [Principes directeurs](#2-principes-directeurs)
3. [Périmètre du MVP](#3-périmètre-du-mvp)
4. [Architecture](#4-architecture)
5. [Spécifications fonctionnelles](#5-spécifications-fonctionnelles)
6. [Modèle de menace](#6-modèle-de-menace)
7. [Exigences non fonctionnelles](#7-exigences-non-fonctionnelles)
8. [Critères d'acceptation du MVP](#8-critères-dacceptation-du-mvp)
9. [Jalons](#9-jalons)
10. [Hors périmètre, et pourquoi](#10-hors-périmètre-et-pourquoi)
11. [Risques et décisions en suspens](#11-risques-et-décisions-en-suspens)
12. [Annexe — pile technique retenue](#12-annexe--pile-technique-retenue)

---

## En une page

**Ce qu'on construit.** Un démon Go qui sauvegarde des bases de données selon un planning, chiffre les archives avec une clé publique, les envoie sur une ou plusieurs destinations, vérifie qu'elles sont relisibles, applique une politique de rétention et alerte quand quelque chose ne va pas. Il expose une interface web locale pour tout consulter et une CLI pour tout piloter.

**Ce qui le distingue.** Il est complet tout seul. Le serveur central est une commodité — une vue de parc à la Uptime Kuma — et non un organe vital. Il ne détient aucun identifiant, ne peut ordonner aucune restauration, et son indisponibilité ne dégrade ni les sauvegardes ni les alertes.

**Le pari technique.** On n'écrit pas nos propres dumpers pour PostgreSQL et MySQL : on appelle `pg_dump` et `mysqldump`, mais on garantit qu'on appelle toujours *la bonne version*, et on refuse de sauvegarder plutôt que de produire une archive douteuse.

---

## 1. Contexte et objectifs

### 1.1 Le problème

Les outils de sauvegarde de bases de données auto-hébergés existants font un choix d'architecture qui pose deux problèmes en entreprise.

D'abord, ils **centralisent les identifiants**. Le serveur d'orchestration détient les accès à toutes les bases du parc et à tous les stockages, souvent chiffrés avec une clé unique qu'il détient également. Sa compromission donne un accès complet aux bases de production — et, plus grave, la capacité d'ordonner des restaurations destructrices sur l'ensemble du parc.

Ensuite, ils **imposent Docker**. Non par nécessité technique, mais parce que c'est le moyen le plus simple d'embarquer la matrice de versions des outils de dump. Cela exclut les serveurs durcis, les politiques interdisant le socket Docker, et les machines de base de données volontairement minimalistes.

### 1.2 Objectifs

- **Autonomie réelle.** Un agent installé seul sur une machine sauvegarde, vérifie, restaure et alerte sans aucune autre infrastructure.
- **Surface de compromission minimale.** Aucun composant du système ne doit, en tombant, donner accès aux données de production.
- **Installation sans conteneur.** Un binaire, un fichier de configuration, un service `systemd`. Docker devient une option, jamais un prérequis.
- **Justesse démontrable.** Chaque archive porte la trace de ce qui l'a produite : version du serveur, version de l'outil, commande exacte, empreinte. On doit pouvoir restaurer dans trois ans sans deviner.
- **Visibilité de parc.** Un serveur central optionnel donne l'état de tous les agents et de toutes les bases, façon Uptime Kuma.

### 1.3 Non-objectifs

- **Réimplémenter `pg_dump` ou `mysqldump` en Go.** Vingt-cinq ans de correctifs ne se rattrapent pas, et un dumper juste à 99 % se découvre le jour du PRA.
- **Remplacer une solution de sauvegarde système** (volumes, machines, fichiers). Le périmètre est la base de données.
- **Faire de la réplication, du PITR ou du streaming WAL.** Ce sont d'autres outils, avec d'autres garanties.
- **Le multi-tenant SaaS.** Le serveur central vise le parc d'une organisation.

---

## 2. Principes directeurs

Ces six principes sont non négociables. Toute décision d'implémentation qui les contredit est à rejeter, même si elle est plus pratique.

| ID | Principe | Détail |
|---|---|---|
| **P1** | **L'agent se suffit à lui-même** | Toute fonction du produit — planification, chiffrement, vérification, rétention, restauration, alerte, consultation — est utilisable sans serveur central. Le serveur n'ajoute que de l'agrégation et du confort. |
| **P2** | **Le serveur central ne détient rien et n'ordonne rien de destructif** | Aucun identifiant de base, aucune clé de stockage, aucune clé de chiffrement ne transite vers lui ni n'y est stocké. Il ne peut pas déclencher de restauration. Son seul pouvoir d'écriture est la modification d'un planning, et l'agent valide chaque modification contre ses propres garde-fous. |
| **P3** | **Le bon outil, prouvé, ou pas de sauvegarde** | La version de l'outil de dump est vérifiée en l'exécutant, jamais déduite d'un chemin. Si aucun outil compatible n'est disponible, le job échoue avec un message actionnable plutôt que de produire une archive douteuse. |
| **P4** | **Rien n'est sauvegardé tant que ce n'est pas vérifié** | Une archive n'est marquée valide qu'après contrôle d'intégrité et vérification qu'elle est structurellement relisible. Une sauvegarde non vérifiée est signalée comme telle dans l'interface. |
| **P5** | **Un binaire, zéro dépendance de service** | Pas de Redis, pas de PostgreSQL de service, pas de runtime externe. L'état tient dans un fichier SQLite. Le binaire est statique et se compile sans CGO. |
| **P6** | **Échouer bruyamment, jamais silencieusement** | La panne la plus dangereuse d'un système de sauvegarde est celle qui ne se voit pas. L'absence de sauvegarde réussie est un événement de premier ordre, au même titre qu'un échec. |

---

## 3. Périmètre du MVP

| Domaine | Dans le MVP | Reporté |
|---|---|---|
| **Moteurs** | PostgreSQL 12–18, MySQL 8.x, MariaDB 10.6+ | MongoDB, SQLite, Redis, Valkey, SQL Server, volumes |
| **Destinations** | Système de fichiers local, S3-compatible, SFTP | Azure Blob, GCS, Google Drive, rclone |
| **Outils de dump** | Détection hôte, installation gérée, `docker exec` | `kubectl exec`, dumpers natifs |
| **Vérification** | Empreinte, relecture de structure d'archive | Restauration de test dans une base jetable |
| **Alertes** | E-mail SMTP, webhook JSON | Slack natif, Discord natif, PagerDuty, SMS |
| **Interface** | Web locale en lecture + actions, CLI complète | Authentification multi-utilisateur, RBAC |
| **Serveur central** | Protocole défini et implémenté côté agent | Le serveur lui-même — lot séparé |

> **Décision de cadrage.** Le serveur central n'est pas dans le MVP. Mais son protocole l'est, et l'agent doit pouvoir s'y connecter dès le premier jour. La raison est qu'un protocole ajouté après coup contamine toujours le modèle de l'agent — on se retrouve à exposer des choses parce que le serveur en a besoin. En le définissant d'abord, la frontière de confiance est posée avant qu'on puisse la contourner par facilité.

---

## 4. Architecture

### 4.1 Chaîne de sauvegarde

| # | Étape | Détail |
|---|---|---|
| 01 | **Résolution** | Version du serveur, choix de l'outil compatible |
| 02 | **Dump** | Sous-process, sortie en flux |
| 03 | **Compression** | zstd, à la volée |
| 04 | **Chiffrement** | age, clé publique |
| 05 | **Écriture** | Destinations en parallèle |
| 06 | **Vérification** | Empreinte et relecture |
| 07 | **Manifeste** | Journal, index, rétention |

Les étapes 02 à 04 sont **toujours** chaînées en flux : le dump brut n'est jamais matérialisé, ni en mémoire ni sur disque. Le point de coupure entre 04 et 05 fait l'objet d'une politique explicite — voir §4.5.

### 4.2 Composants internes

| Composant | Rôle |
|---|---|
| `scheduler` | Planificateur cron interne, persiste le dernier et le prochain déclenchement en SQLite |
| `resolver` | Détecte les outils disponibles, en vérifie la version, choisit celui à utiliser par base et par opération |
| `engine` | Une implémentation par moteur : sonde, dump, restauration, nettoyage pré-restauration |
| `pipeline` | Chaîne compression, chiffrement, calcul d'empreinte et écriture multi-destination |
| `store` | Abstraction de destination : écriture, lecture, listage, suppression, reprise |
| `catalog` | Index des archives et de leurs manifestes, source de vérité de la rétention |
| `notifier` | Règles d'alerte, anti-répétition, canaux, bascule vers le serveur central |
| `uplink` | Envoi d'état au serveur central, réception et validation du planning |
| `httpd` | Interface web locale et API JSON qui l'alimente |

### 4.3 Arborescence sur disque

```
/etc/keeper/
  keeper.yaml              # configuration, lue à chaud
  recipients.txt           # clés publiques age autorisées

/var/lib/keeper/
  keeper.db                # état SQLite (jobs, catalogue, planning)
  tools/
    postgresql/16/bin/     # outils installés par l'agent
    postgresql/17/bin/
    mariadb/11.4/bin/
  tmp/                     # espace de travail des jobs, purgé au démarrage

/var/log/keeper/
  keeper.log               # journal applicatif, rotation interne
```

### 4.4 État local

SQLite via `modernc.org/sqlite` — implémentation pure Go, qui permet de conserver `CGO_ENABLED=0` et donc un binaire statique réellement portable. Tables principales :

- `databases` — instantané de la configuration résolue, pour détecter les changements
- `jobs` — un enregistrement par exécution : type, base, statut, horodatage, durée, code de sortie
- `job_logs` — lignes de journal structurées rattachées à un job
- `backups` — le catalogue : identifiant, base, manifeste, empreintes, état de vérification
- `backup_locations` — une ligne par couple archive/destination, avec chemin distant et taille
- `schedules` — expression cron effective, origine (locale ou serveur), dernier et prochain déclenchement
- `alerts` — événements émis, pour l'anti-répétition et l'historique

### 4.5 Politique de tampon

Le dump brut n'est jamais écrit sur disque : une base de 40 Go ne produit à aucun moment un fichier de 40 Go. Mais pousser le flux jusque dans la socket de la destination distante, sans aucune escale, coûte quatre choses qu'un outil de sauvegarde ne peut pas se permettre par défaut.

- **Une coupure réseau impose de tout recommencer.** Un flux ne se rembobine pas : un échec à 38 Go sur 40 relance le dump entier, et donc la charge sur la base de production.
- **La vérification devient payante.** Contrôler l'empreinte et la structure d'une archive qui n'existe qu'à distance suppose de la retélécharger — de l'egress facturé, chaque nuit.
- **Les destinations se contaminent.** En alimentant plusieurs destinations depuis une seule lecture, la plus lente impose son rythme à toute la chaîne, `pg_dump` compris — donc allonge la durée de la transaction ouverte sur la base et fait gonfler le MVCC.
- **Le format répertoire est exclu.** `-Fd`, prescrit en F3.5 au-delà d'un seuil, écrit une arborescence et ne peut pas sortir sur un flux.

L'élément décisif : ce qui est mis en tampon n'est pas le dump brut mais le flux **déjà compressé et chiffré**. Une base de 40 Go occupe typiquement 4 à 10 Go après zstd, soit 10 à 25 % de la taille brute. Et si `filesystem` figure parmi les destinations, ce fichier tampon *est* la copie locale : le surcoût disque est alors nul.

| Mode | Comportement | Retenu quand |
|---|---|---|
| `stage` | Le flux compressé et chiffré est écrit dans un fichier tampon, puis envoyé vers chaque destination depuis ce fichier. La transaction sur la base se ferme dès la fin du dump. | **Défaut.** Espace disponible ≥ taille compressée estimée × 1,5 |
| `stream` | Le flux va directement à la destination. Aucune escale, aucune reprise possible, vérification par retéléchargement. | Disque insuffisant, ou une seule destination distante avec vérification différée assumée |
| `auto` | Arbitre à chaque exécution selon l'espace disponible, le nombre de destinations, le format de dump et la politique de vérification. | Valeur recommandée en configuration |

> **Règle.** Le mode `stage` est obligatoire dès que le format `-Fd` est retenu, dès qu'il y a plus d'une destination, ou dès que la vérification structurelle est exigée sans egress. `stream` reste un choix explicite pour les machines à disque contraint, et `doctor` indique pour chaque base le mode qui sera effectivement appliqué.

---

## 5. Spécifications fonctionnelles

Chaque exigence porte un identifiant stable, réutilisable dans les tickets et les tests. **Obligatoire** conditionne la livraison du MVP ; **Souhaitable** est à livrer si le calendrier le permet.

### 5.1 Configuration — `F1`

| ID | Exigence | Niveau |
|---|---|---|
| **F1.1** | Configuration en un fichier YAML unique, avec analyse stricte : toute clé inconnue est une erreur, pas un avertissement. Une faute de frappe ne doit jamais désactiver silencieusement une sauvegarde. | Obligatoire |
| **F1.2** | Les secrets ne sont jamais obligatoirement en clair dans le fichier : trois formes acceptées par champ sensible — valeur littérale, `*_env` (variable d'environnement), `*_file` (fichier, compatible `systemd` credentials et Vault Agent). | Obligatoire |
| **F1.3** | `keeper config validate` vérifie la syntaxe, la cohérence, la joignabilité des bases et l'existence des outils, sans rien exécuter d'autre. | Obligatoire |
| **F1.4** | Relecture à chaud sur `SIGHUP` et sur changement de mtime. Une configuration invalide est rejetée et l'ancienne conservée, avec une alerte. | Souhaitable |
| **F1.5** | Le fuseau horaire des plannings est déclaré explicitement dans la configuration, jamais hérité de l'environnement système. | Obligatoire |

#### Forme cible

```yaml
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
  max_parallel_jobs: 2

encryption:
  recipients_file: /etc/keeper/recipients.txt   # clés publiques age

databases:
  - id: boutique-prod
    engine: postgresql
    host: 10.0.3.12
    port: 5432
    database: boutique
    user: keeper_backup
    password_file: /run/credentials/keeper.service/boutique
    tools: auto
    staging: auto                    # stage | stream | auto — voir §4.5
    schedule: "0 2 * * *"
    min_interval: 24h                # plancher opposable au serveur central
    allow_remote_schedule: true
    allow_restore: false             # restauration refusée tant que non armée
    destinations: [disque-local, s3-ovh]
    retention:
      last: 7
      daily: 14
      weekly: 8
      monthly: 12

  - id: erp-prod
    engine: mariadb
    host: 127.0.0.1
    port: 3306
    database: erp
    user: keeper_backup
    password_env: ERP_BACKUP_PASSWORD
    tools:
      strategy: exec                 # dump via le conteneur de la base
      container: erp-mariadb
    schedule: "30 1 * * *"
    destinations: [disque-local, sftp-nas]

destinations:
  - id: disque-local
    type: filesystem
    path: /srv/backups
  - id: s3-ovh
    type: s3
    endpoint: https://s3.gra.io.cloud.ovh.net
    region: gra
    bucket: keeper-prod
    access_key_id_env: OVH_ACCESS_KEY
    secret_access_key_env: OVH_SECRET_KEY
  - id: sftp-nas
    type: sftp
    host: nas.interne.exemple.fr
    port: 22
    user: keeper
    private_key_file: /etc/keeper/sftp_ed25519
    path: /volume1/backups

alerts:
  channels:
    - id: ops-mail
      type: email
      to: [ops@exemple.fr]
      smtp: { host: smtp.exemple.fr, port: 587, user: keeper, password_env: SMTP_PASSWORD }
    - id: ops-hook
      type: webhook
      url: https://hooks.exemple.fr/keeper
  rules:
    - on: [backup_failed, backup_missed, verify_failed, database_unreachable, tool_missing]
      channels: [ops-mail, ops-hook]

server:
  enabled: false
  url: https://keeper.interne.exemple.fr
  token_file: /etc/keeper/uplink.token
  delegate_alerts: true
  fallback_after: 15m               # reprise des alertes locales si injoignable
```

### 5.2 Résolution des outils — `F2`

C'est le cœur technique du produit et la principale source de bugs silencieux dans les outils concurrents. Le principe : **la compatibilité prime sur la provenance.**

| ID | Exigence | Niveau |
|---|---|---|
| **F2.1** | Énumérer *toutes* les sources sans préférence initiale : chemins système connus par distribution, `PATH`, répertoire géré par l'agent, sortie de `pg_lsclusters` si présente, et conteneur de la base si la stratégie `exec` est autorisée. | Obligatoire |
| **F2.2** | Obtenir la version de chaque candidat **en l'exécutant** (`pg_dump --version`), jamais en interprétant son chemin. Le résultat est mis en cache, invalidé sur changement de mtime du binaire. | Obligatoire |
| **F2.3** | Filtrer selon la règle de compatibilité de l'opération demandée (voir matrice), puis retenir le candidat dont la version est la plus proche par le haut. À version égale, préférer l'hôte, puis l'outil géré, puis le conteneur. | Obligatoire |
| **F2.4** | Ne jamais croiser les familles MySQL et MariaDB. `mysqldump` d'Oracle pour MySQL, `mariadb-dump` pour MariaDB — la famille est détectée à la connexion, pas déduite de la configuration. | Obligatoire |
| **F2.5** | En l'absence de candidat compatible, échouer avec un message nommant la version attendue, les versions trouvées et la commande exacte pour corriger. Jamais de repli sur un outil incompatible. | Obligatoire |
| **F2.6** | Installation d'outils à la demande via `keeper tools install`, dans `/var/lib/keeper/tools/`, avec bibliothèques partagées embarquées et `RPATH` ajusté. | Obligatoire |
| **F2.7** | Toute archive téléchargée est vérifiée contre une empreinte SHA-256 épinglée dans la version de l'agent, et sa signature contrôlée avant première exécution. | Obligatoire |
| **F2.8** | L'installation automatique à la découverte d'une version inconnue est **désactivée par défaut**. Si activée, elle est bornée par une liste blanche de versions et tracée comme événement. | Obligatoire |
| **F2.9** | La stratégie `exec` exige le socket Docker et se déclare **par base**, jamais globalement. | Obligatoire |

#### Matrice de compatibilité

| Opération | Règle | Pourquoi |
|---|---|---|
| PostgreSQL — dump | `major(pg_dump) ≥ major(serveur)` | `pg_dump` sauvegarde un serveur plus ancien, jamais plus récent — il refuse et sort en erreur |
| PostgreSQL — restauration | `major(pg_restore) ≥ major(archive)` | Le format d'archive évolue ; un `pg_restore` ancien ne sait pas lire une archive récente |
| PostgreSQL — cible | `major(cible) ≥ major(origine)` conseillé | La restauration descendante n'est pas garantie ; à signaler, pas à bloquer |
| MySQL / MariaDB | Famille identique, `version(client) ≥ version(serveur)` | Plugins d'authentification et options divergentes entre les deux familles |

> **Piège à éviter.** La tentation naturelle est d'écrire « priorité aux outils de l'hôte ». Cela produit le bug suivant : l'hôte a `pg_dump` 14, la base est en 16, l'agent a déjà installé le client 16 — et il choisit le 14. La provenance doit être un critère de *départage* entre candidats valides, jamais un critère de sélection.

### 5.3 Sauvegarde — `F3`

| ID | Exigence | Niveau |
|---|---|---|
| **F3.1** | Un seul job actif par base à tout instant, garanti par un verrou local. Une demande concurrente est refusée, pas mise en file. | Obligatoire |
| **F3.2** | Nombre de jobs parallèles global plafonné par configuration, pour ne pas saturer un serveur de bases. | Obligatoire |
| **F3.3** | Le dump brut n'est jamais matérialisé, ni en mémoire ni sur disque : compression, chiffrement et empreinte sont calculés en une seule passe sur le flux sortant du sous-process. | Obligatoire |
| **F3.4** | Le point de coupure vers les destinations suit la politique de tampon de §4.5, mode `auto` par défaut. Le mode effectivement appliqué est enregistré dans le manifeste et affiché par `doctor`. | Obligatoire |
| **F3.5** | PostgreSQL : format `-Fc` par défaut. Le format répertoire `-Fd` avec parallélisme est utilisé au-delà d'un seuil configurable — il impose alors le mode `stage`, et reste indisponible en stratégie `exec`, ce que l'agent doit signaler explicitement. | Obligatoire |
| **F3.6** | En mode `stage`, la connexion à la base est fermée dès la fin du dump, sans attendre la fin des envois — la durée d'une transaction ouverte sur la production ne doit jamais dépendre de la bande passante d'une destination distante. | Obligatoire |
| **F3.7** | MySQL et MariaDB : `--single-transaction` par défaut sur InnoDB, avec routines, déclencheurs et événements. La présence de tables MyISAM est détectée et signalée, car elle invalide la cohérence transactionnelle. | Obligatoire |
| **F3.8** | Chaque job produit un manifeste JSON non chiffré et sans secret, stocké à côté de l'archive sur chaque destination. | Obligatoire |
| **F3.9** | Sur `SIGTERM`, un dump en cours dispose d'un délai de grâce configurable pour se terminer ; passé ce délai il est interrompu et le job marqué en échec — jamais en succès. | Obligatoire |
| **F3.10** | Espace disque estimé et vérifié avant de commencer, avec marge : taille compressée attendue extrapolée de la dernière sauvegarde réussie, à défaut de la taille de la base. Un job qui remplirait le disque bascule en `stream` ou est refusé en amont. | Obligatoire |

#### Manifeste d'archive

```json
{
  "backup_id": "01JQ8F3K2M7X9P4W",
  "database_id": "boutique-prod",
  "engine": "postgresql",
  "started_at": "2026-09-18T02:00:03+02:00",
  "duration_ms": 184320,
  "server_version": "16.4",
  "tool": {
    "name": "pg_dump",
    "version": "16.4",
    "source": "managed",
    "path": "/var/lib/keeper/tools/postgresql/16/bin/pg_dump",
    "argv": ["--format=custom", "--no-owner", "--no-privileges", "--compress=0"]
  },
  "format": "pg_custom",
  "pipeline": ["zstd:3", "age:x25519"],
  "staging": "stage",
  "size_raw": 4187593216,
  "size_stored": 512884736,
  "sha256_raw": "9f2c…a71b",
  "sha256_stored": "3ade…c024",
  "recipients": ["age1ql3z…mcac8p"],
  "verified": { "checksum": true, "structure": true, "at": "2026-09-18T02:03:21+02:00" }
}
```

Le manifeste ne contient **jamais** d'identifiant de connexion. Il doit rester lisible sans la clé privée, pour permettre l'inventaire d'un dépôt d'archives.

### 5.4 Vérification — `F4`

| ID | Exigence | Niveau |
|---|---|---|
| **F4.1** | Empreinte SHA-256 calculée au vol pendant l'écriture, puis recalculée à la relecture de la destination pour au moins une destination par sauvegarde. | Obligatoire |
| **F4.2** | Vérification structurelle : pour PostgreSQL, `pg_restore --list` sur l'archive doit produire une table des matières cohérente. Pour MySQL et MariaDB, contrôle du marqueur de fin de dump et décompression complète sans erreur. | Obligatoire |
| **F4.3** | Une archive non vérifiée est visuellement distincte d'une archive vérifiée dans l'interface et la CLI. L'absence de vérification n'est jamais assimilée à un succès. | Obligatoire |
| **F4.4** | Vérification périodique d'archives anciennes choisies au hasard, pour détecter la corruption silencieuse du stockage. | Souhaitable |

### 5.5 Destinations — `F5`

| ID | Exigence | Niveau |
|---|---|---|
| **F5.1** | Une interface unique — écrire en flux, lire en flux, lister, supprimer, tester l'accès — implémentée par les trois destinations du MVP. | Obligatoire |
| **F5.2** | Écriture parallèle sur plusieurs destinations à partir d'une seule lecture du flux ou du fichier tampon, sans repasser par le dump. | Obligatoire |
| **F5.3** | L'échec d'une destination n'annule pas les autres. Le job est réussi si au moins une destination a reçu et vérifié l'archive ; l'état par destination est conservé et alerté. | Obligatoire |
| **F5.4** | S3 : téléversement en parties avec reprise et taille de partie adaptative, compatibilité explicitement testée avec MinIO, Scaleway, OVH et Backblaze — pas seulement AWS. | Obligatoire |
| **F5.5** | Chemin distant déterministe et lisible par un humain : `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<ext>`. Un dépôt doit rester exploitable si l'agent disparaît. | Obligatoire |
| **F5.6** | Documentation d'une politique S3 en écriture seule et de l'activation d'Object Lock, avec les conséquences sur la rétention. | Souhaitable |

### 5.6 Chiffrement — `F6`

Choix de conception structurant : **chiffrement asymétrique** via `age`. L'agent détient une clé *publique* ; la clé privée vit hors de la machine.

| ID | Exigence | Niveau |
|---|---|---|
| **F6.1** | Chiffrement en flux avec `filippo.io/age`, destinataires déclarés par clé publique X25519. | Obligatoire |
| **F6.2** | Plusieurs destinataires possibles par base — clé opérationnelle et clé de séquestre — pour qu'une clé perdue ne condamne pas les archives. | Obligatoire |
| **F6.3** | La clé privée n'est jamais requise par l'agent, ni présente dans sa configuration. Le déchiffrement est une opération manuelle et distincte. | Obligatoire |
| **F6.4** | Une archive chiffrée doit rester déchiffrable avec l'outil `age` standard, sans Keeper. Aucun format maison. | Obligatoire |
| **F6.5** | `keeper keygen` génère une paire, affiche la clé publique et **n'écrit jamais la clé privée sur la machine de l'agent** ; elle est affichée une fois, à charge de l'opérateur de la mettre à l'abri. | Obligatoire |

> **Pourquoi asymétrique.** Avec une clé symétrique partagée, l'agent peut déchiffrer tout ce qu'il a produit. Un attaquant qui obtient la machine obtient donc l'historique complet des sauvegardes, en plus de l'accès courant aux bases. Avec une clé publique, il n'obtient que ce qu'il peut déjà voir : les bases telles qu'elles sont maintenant. L'historique reste hors de portée. Le coût est opérationnel — il faut gérer une clé privée avec sérieux — et c'est un coût qu'un outil de sauvegarde doit assumer.

### 5.7 Rétention — `F7`

| ID | Exigence | Niveau |
|---|---|---|
| **F7.1** | Politique de type grand-père / père / fils : `last`, `daily`, `weekly`, `monthly`. Une archive retenue par une seule règle est conservée. | Obligatoire |
| **F7.2** | **Aucune suppression si la dernière sauvegarde a échoué**, ou s'il resterait moins d'archives valides que le plancher configuré. La rétention ne doit jamais aggraver une panne. | Obligatoire |
| **F7.3** | Seules les archives vérifiées comptent dans le décompte de rétention. | Obligatoire |
| **F7.4** | `--dry-run` disponible et affiché avant toute première application réelle sur une base donnée. | Obligatoire |
| **F7.5** | Toute suppression est journalisée avec la règle qui l'a motivée, et conservée dans l'historique même après disparition de l'archive. | Obligatoire |

### 5.8 Restauration — `F8`

| ID | Exigence | Niveau |
|---|---|---|
| **F8.1** | Une restauration est **toujours** initiée localement — CLI ou interface web locale. Aucune commande distante ne peut la déclencher, quelle qu'en soit la source. | Obligatoire |
| **F8.2** | Confirmation explicite obligatoire, avec rappel de la base cible et du volume concerné. Contournable en mode non interactif par un drapeau dédié, jamais par `--yes` seul. | Obligatoire |
| **F8.3** | Contrôle préalable : outil compatible, connectivité, droits suffisants, espace disponible, archive déchiffrable. Échec en amont plutôt qu'à mi-parcours. | Obligatoire |
| **F8.4** | Modes de nettoyage explicites et ordonnés du moins au plus destructif : `none`, `clean`, `drop-schemas`, `drop-database`. Défaut : `clean`. | Obligatoire |
| **F8.5** | Restauration vers une cible différente de l'origine (`--into`), pour la recette et la migration, sans toucher la base d'origine. | Obligatoire |
| **F8.6** | `allow_restore: false` par défaut ; l'armement se fait par base et expire après un délai configurable. | Souhaitable |

### 5.9 Planification — `F9`

| ID | Exigence | Niveau |
|---|---|---|
| **F9.1** | Planificateur interne au processus, sans dépendance externe. Expressions cron à cinq champs, dans le fuseau déclaré. | Obligatoire |
| **F9.2** | Le dernier et le prochain déclenchement sont persistés. Au démarrage, une échéance manquée pendant l'arrêt déclenche l'événement `backup_missed` — et **non** une rafale de rattrapage. | Obligatoire |
| **F9.3** | Décalage aléatoire configurable par base, pour éviter que quarante agents ne frappent leur stockage à 2 h 00 pile. | Souhaitable |
| **F9.4** | Le plancher `min_interval` est opposable à toute modification distante : une fréquence proposée plus lâche que le plancher est rejetée et alertée. | Obligatoire |

### 5.10 Alertes — `F10`

L'événement le plus important du produit n'est pas `backup_failed` mais `backup_missed` : un échec se voit, une absence ne se voit pas.

| Événement | Déclencheur | Gravité |
|---|---|---|
| `backup_missed` | Aucune sauvegarde réussie depuis l'intervalle attendu majoré d'une tolérance | Critique |
| `backup_failed` | Job terminé en échec | Critique |
| `verify_failed` | Empreinte ou structure invalide | Critique |
| `database_unreachable` | Sonde en échec N fois consécutives | Avertissement |
| `destination_failed` | Une destination sur plusieurs a échoué | Avertissement |
| `tool_missing` | Aucun outil compatible pour une base planifiée | Avertissement |
| `retention_blocked` | Rétention suspendue par la règle de sûreté | Avertissement |
| `uplink_lost` | Serveur central injoignable au-delà du seuil | Information |
| `remote_command_rejected` | Le serveur a envoyé une instruction interdite | Critique |

| ID | Exigence | Niveau |
|---|---|---|
| **F10.1** | Canaux MVP : e-mail SMTP et webhook JSON générique. Un webhook générique couvre Slack, Discord, Teams et Mattermost sans code dédié. | Obligatoire |
| **F10.2** | Anti-répétition : une alerte n'est réémise que si l'état change, ou après un délai de rappel configurable. Une base en panne trois jours ne produit pas 4 320 e-mails. | Obligatoire |
| **F10.3** | Un événement de rétablissement est émis au retour à la normale. | Obligatoire |
| **F10.4** | Avec `delegate_alerts: true`, le serveur central prend en charge la notification. L'agent conserve un repli : si le serveur est injoignable depuis `fallback_after`, il réémet localement, y compris les événements survenus pendant la coupure. | Obligatoire |
| **F10.5** | `remote_command_rejected` est toujours notifié localement, jamais délégué — c'est un signal de compromission potentielle du serveur. | Obligatoire |

### 5.11 Interface web locale — `F11`

| ID | Exigence | Niveau |
|---|---|---|
| **F11.1** | Servie par le binaire lui-même, ressources embarquées via `embed.FS`. Aucune construction d'actifs au démarrage, aucun accès réseau sortant pour l'afficher. | Obligatoire |
| **F11.2** | `keeper ui` écoute sur `127.0.0.1` par défaut et ouvre le navigateur. Toute écoute sur une autre interface exige une authentification configurée, sinon l'agent refuse de démarrer. | Obligatoire |
| **F11.3** | Vues : tableau de bord du parc local, fiche par base, historique des jobs, journal détaillé d'un job, catalogue des archives, prochaines exécutions. | Obligatoire |
| **F11.4** | Actions : déclencher une sauvegarde, vérifier une archive, appliquer la rétention en simulation, lancer une restauration avec confirmation. | Obligatoire |
| **F11.5** | Rendu serveur, interactions par HTMX. Pas de SPA, pas de chaîne de compilation JavaScript — cohérent avec l'objectif du binaire unique. | Souhaitable |

### 5.12 Surface CLI — `F12`

```
keeper serve                                  # démon : planificateur + interface
keeper ui [--listen 127.0.0.1:8765]           # interface seule, ouvre le navigateur
keeper doctor [--database ID]                 # diagnostic complet avant incident

keeper backup <db> [--dry-run]
keeper list [<db>] [--destination ID]
keeper verify <backup-id>
keeper restore <db> --from <backup-id> [--into DSN] [--clean-mode MODE]
keeper retention apply [<db>] [--dry-run]

keeper tools list
keeper tools install <engine> <version>
keeper tools remove <engine> <version>

keeper config validate [--file PATH]
keeper config show [--redact]
keeper keygen

keeper version [--json]
```

`keeper doctor` est la commande la plus importante de la liste. Elle affiche, pour chaque base : joignabilité, version du serveur, outil retenu avec sa version et sa provenance, mode de tampon qui sera appliqué, destinations accessibles, prochaine exécution, état de la dernière sauvegarde. Aujourd'hui, dans les outils existants, on découvre ces problèmes dans les journaux d'un job de nuit.

### 5.13 Liaison au serveur central — `F13`

| ID | Exigence | Niveau |
|---|---|---|
| **F13.1** | L'agent **pousse** vers le serveur en HTTPS. Le serveur n'ouvre jamais de connexion vers l'agent, qui n'expose donc aucun port au-delà de son interface locale. | Obligatoire |
| **F13.2** | Authentification par jeton propre à l'agent, lu depuis un fichier. mTLS accepté en option. | Obligatoire |
| **F13.3** | Contenu poussé : identité et version de l'agent, liste des bases avec état, résultats de jobs, événements, prochaines échéances. **Jamais** d'identifiant, de clé ou de contenu de configuration sensible. | Obligatoire |
| **F13.4** | Seul champ que le serveur peut modifier : l'expression cron d'une base dont `allow_remote_schedule` vaut `true`. Tout autre champ reçu est ignoré. | Obligatoire |
| **F13.5** | Une réponse du serveur contenant une instruction hors du champ autorisé — restauration, configuration, identifiant, installation d'outil — est rejetée, journalisée et notifiée localement comme incident de sécurité. | Obligatoire |
| **F13.6** | Le niveau de détail des journaux transmis est configurable, avec masquage systématique des chaînes de connexion et des valeurs sensibles. | Souhaitable |
| **F13.7** | L'indisponibilité du serveur n'a aucun effet sur les sauvegardes. Les événements sont mis en file localement et retransmis au retour. | Obligatoire |

---

## 6. Modèle de menace

Cette section est le contrat de sécurité du produit. Elle énonce ce qu'un attaquant obtient dans chaque scénario, et ce qu'il n'obtient pas.

| Scénario | L'attaquant obtient | Il n'obtient pas |
|---|---|---|
| **Serveur central compromis** | La vue du parc. La capacité de mentir sur un tableau de bord. La modification des plannings, dans la limite des planchers locaux. | Aucun identifiant de base ou de stockage. Aucune capacité de restauration. Aucune clé. Aucun accès réseau aux bases. |
| **Agent compromis** | L'accès aux bases qu'il gère — irréductible, c'est sa fonction. La capacité d'écrire de nouvelles archives. | Le déchiffrement des archives passées : il ne détient qu'une clé publique. |
| **Stockage compromis** | Des archives chiffrées et leurs manifestes — donc des métadonnées : noms de bases, tailles, horaires. | Le contenu des archives. Les identifiants, absents des manifestes. |
| **Fichier de configuration lu** | La topologie du parc : hôtes, ports, noms de bases. | Les mots de passe, si les formes `*_file` ou `*_env` sont utilisées. |

### 6.1 Ce que le serveur central peut et ne peut pas

**Autorisé**

- Lire l'état des agents et des bases
- Lire l'historique et les journaux des jobs
- Émettre les notifications déléguées
- Proposer une expression cron, sur les bases qui l'acceptent

**Refusé par conception**

- Déclencher une restauration
- Transmettre ou détenir un identifiant
- Modifier une configuration de base ou de destination
- Assouplir un plancher de fréquence
- Demander l'installation ou le choix d'un outil

### 6.2 Recommandations de déploiement à documenter

- Utilisateur de base dédié, en lecture seule pour la sauvegarde, distinct de l'utilisateur de restauration.
- Object Lock ou versionnement sur le stockage distant, avec des clés en écriture seule pour l'agent. C'est la mesure au meilleur rendement du document : elle rend survivable le scénario destructif.
- Clé privée de déchiffrement conservée hors des machines de production, avec une clé de séquestre distincte et une procédure de restauration testée.
- Le serveur central n'a aucune route réseau vers les bases de données.

---

## 7. Exigences non fonctionnelles

| ID | Exigence |
|---|---|
| **N1** | Binaire statique, `CGO_ENABLED=0`, pour `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`. Cible : moins de 30 Mo. |
| **N2** | Empreinte mémoire au repos inférieure à 50 Mo, et indépendante de la taille des bases sauvegardées — conséquence directe du traitement en flux. |
| **N3** | Aucun service tiers requis : ni base de données externe, ni file de messages, ni runtime. |
| **N4** | Unité `systemd` fournie, avec durcissement : utilisateur dédié, `ProtectSystem`, `NoNewPrivileges`, `PrivateTmp`. |
| **N5** | Journaux structurés en JSON vers la sortie standard, plus un fichier avec rotation interne. Compatibles `journald` sans configuration. |
| **N6** | Arrêt propre : plus aucun nouveau job après `SIGTERM`, délai de grâce pour les jobs en cours, état cohérent en base quoi qu'il arrive. |
| **N7** | Tests d'intégration réels contre PostgreSQL 13 à 18 et MariaDB 10.6, 10.11 et 11.4 via conteneurs éphémères, incluant systématiquement l'aller-retour sauvegarde puis restauration. |
| **N8** | Une image conteneur est publiée en complément du binaire — pour ceux qui la préfèrent, jamais comme prérequis. |

---

## 8. Critères d'acceptation du MVP

Le MVP est livré quand les onze scénarios suivants passent sur une machine vierge, **sans Docker installé** (sauf le scénario 4, qui le requiert par nature).

1. Installation du binaire, rédaction de la configuration et première sauvegarde réussie d'un PostgreSQL 16 en moins de dix minutes.
2. Une base en PostgreSQL 17 alors que seul le client 15 est présent : le job échoue avec un message nommant la version manquante et la commande d'installation.
3. Après `keeper tools install postgresql 17`, la même sauvegarde réussit sans modifier la configuration.
4. Une base MariaDB conteneurisée, port non publié, sauvegardée via la stratégie `exec`.
5. Restauration complète d'une base de 5 Go depuis S3 vers une instance vierge, avec égalité vérifiée du nombre de lignes de chaque table.
6. L'archive stockée est illisible sans la clé privée, et déchiffrable avec l'outil `age` standard, sans Keeper.
7. Arrêt de l'agent pendant vingt-quatre heures : au redémarrage, un unique `backup_missed` est émis, sans rafale de rattrapage.
8. Base rendue injoignable : une seule alerte, un rappel après le délai configuré, puis un événement de rétablissement au retour.
9. Rétention appliquée sur trente archives simulées, conforme à la politique, et suspendue dès lors que la dernière sauvegarde est en échec.
10. Un serveur central simulé envoyant un ordre de restauration : l'ordre est rejeté, journalisé, et notifié localement comme incident.
11. Sauvegarde d'une base de 40 Go en mode `stage` : aucun fichier de plus de la taille compressée n'apparaît sur disque, la connexion à la base se ferme avant le début de l'envoi, et une coupure réseau simulée pendant l'envoi est reprise sans relancer le dump.

> **Critère transversal.** Le scénario 10 n'est pas un test parmi d'autres : c'est la vérification que le contrat de sécurité du document est réellement implémenté, et non seulement écrit. Il doit exister dès le premier lot qui active la liaison au serveur.

---

## 9. Jalons

Découpage en lots livrables et démontrables. Chacun produit un binaire utilisable — aucun lot n'est une couche d'infrastructure sans valeur visible.

| Lot | Titre | Contenu |
|---|---|---|
| **L0** | Socle et résolution des outils | Configuration, état SQLite, CLI, `doctor`, moteur de résolution complet avec matrice de compatibilité. Aucune sauvegarde encore — mais `keeper doctor` donne déjà un rapport juste sur un parc réel. C'est le lot qui valide la thèse technique du produit. |
| **L1** | Sauvegarde locale de bout en bout | PostgreSQL et MariaDB, dump en flux, zstd, chiffrement `age`, politique de tampon, destination disque, manifeste, vérification, catalogue. Sauvegarde déclenchée à la main. |
| **L2** | Restauration et destinations distantes | Restauration avec contrôles préalables et modes de nettoyage, S3 et SFTP, écriture parallèle, reprise de téléversement. À ce stade l'outil est utilisable en production. |
| **L3** | Automatisation | Planificateur, rétention avec règles de sûreté, alertes e-mail et webhook, anti-répétition, `backup_missed`. L'outil devient un système de sauvegarde et non un script. |
| **L4** | Interface locale | Interface web embarquée, vues et actions. Unité `systemd`, paquets, documentation d'installation. |
| **L5** | Liaison au serveur central | Protocole de poussée, validation stricte des réponses, délégation des alertes avec repli, rejet et notification des instructions interdites. Le serveur lui-même fait l'objet d'un cahier des charges distinct. |

---

## 10. Hors périmètre, et pourquoi

| Écarté du MVP | Raison |
|---|---|
| Dumpers natifs en Go | Aucun gain tant que PostgreSQL et MySQL dominent le périmètre, et un risque de correction inacceptable sur un outil de sauvegarde. À rouvrir pour SQLite, Redis et Valkey, où le natif est trivial et sans risque. |
| Restauration de test automatique | C'est la vérification qui a le plus de valeur — la seule qui prouve vraiment qu'une archive est restaurable. Mais elle suppose de provisionner une instance jetable, donc Docker ou un serveur dédié. À concevoir en v2, comme option. |
| PITR et sauvegarde physique | Garanties et complexité d'un autre ordre. Relève d'un produit distinct, pas d'une option. |
| Multi-utilisateur et RBAC | L'interface locale est déjà protégée par l'accès à la machine. La gestion des droits appartient au serveur central. |
| Déduplication et sauvegarde incrémentale | Change entièrement le format de stockage. À décider avant la v1 si l'objectif est de concurrencer les outils à dépôt, après si l'objectif est la fiabilité du dump logique. |

---

## 11. Risques et décisions en suspens

| Risque | Gravité | Traitement |
|---|---|---|
| **Édition de liens des outils installés.** `pg_dump` dépend de libpq, OpenSSL, zlib, ICU. Une archive extraite peut ne pas démarrer sur une distribution dont la glibc diffère. | Élevée | Embarquer les bibliothèques partagées et ajuster le `RPATH`. **À valider par un essai réel sur Debian, Rocky et Alpine avant L0** — c'est le risque technique numéro un du document. |
| **Distribution des outils.** Construire et publier les binaires pour sept versions de PostgreSQL et trois de MariaDB, sur deux architectures. | Moyenne | Chaîne d'intégration dédiée, publication par versions figées, empreintes épinglées dans la version de l'agent. Coût récurrent à assumer. |
| **Perte de la clé privée.** Le chiffrement asymétrique déplace le risque vers la gestion de clé. | Élevée | Destinataires multiples obligatoires dès la configuration initiale, procédure de séquestre documentée, avertissement au premier démarrage tant qu'une seule clé est déclarée. |
| **Cohérence MyISAM.** `--single-transaction` ne garantit rien sur les tables non transactionnelles. | Moyenne | Détection à la sonde, avertissement explicite dans le manifeste et dans l'interface. Ne pas prétendre à une cohérence qu'on n'a pas. |
| **Format répertoire indisponible en mode `exec`.** Le parallélisme de `-Fd` est incompatible avec une sortie en flux. | Faible | Arbitrage explicite documenté : en `exec`, `-Fc` en flux unique. Signalé dans `doctor` pour les bases volumineuses. |
| **Limite de parties S3.** Un téléversement en parties plafonne à 10 000 parties : une taille de partie fixe de 8 Mo bloque à 80 Go d'archive. | Moyenne | Taille de partie adaptative, calculée depuis la taille attendue en mode `stage` et révisée en cours de route en mode `stream`, où la taille finale est inconnue au départ. |

### 11.1 Décisions à prendre avant L0

- **Nom et licence.** « Keeper » est un nom de travail. La licence conditionne la contribution externe et l'usage commercial.
- **Déduplication.** Trancher maintenant : elle change le format de stockage et ne se rattrape pas après coup.
- **Portée de Windows.** Le binaire compile, mais la matrice de tests et les chemins d'outils doublent le travail. Le supporter ou l'annoncer comme non supporté — pas d'entre-deux.
- **Modèle du serveur central.** Son cahier des charges doit commencer dès que L3 est engagé, pour que le protocole se stabilise avec un vrai consommateur en face.

---

## 12. Annexe — pile technique retenue

Indicative, à confirmer au moment d'écrire le `go.mod`. Le critère de sélection est la compatibilité avec `CGO_ENABLED=0` (N1).

| Besoin | Bibliothèque | Remarque |
|---|---|---|
| État local | `modernc.org/sqlite` | Pure Go. **Ne pas utiliser `mattn/go-sqlite3`**, qui impose CGO et casse N1. |
| Chiffrement | `filippo.io/age` | Format standard, déchiffrable par l'outil `age` (F6.4). |
| Compression | `github.com/klauspost/compress/zstd` | Pure Go, performant. |
| S3 | `github.com/aws/aws-sdk-go-v2` | Compatible S3 tiers via endpoint personnalisé (F5.4). |
| SFTP | `github.com/pkg/sftp` + `golang.org/x/crypto/ssh` | — |
| Cron | `github.com/robfig/cron/v3` | Cinq champs, support des fuseaux. |
| CLI | `github.com/spf13/cobra` | — |
| Configuration | `gopkg.in/yaml.v3` avec `KnownFields(true)` | Indispensable pour l'analyse stricte de F1.1. |
| Pilote PostgreSQL | `github.com/jackc/pgx/v5` | Pour la sonde et la détection de version, pas pour le dump. |
| Pilote MySQL/MariaDB | `github.com/go-sql-driver/mysql` | Idem. Sert aussi à détecter la famille (F2.4). |
| Docker | `github.com/docker/docker/client` | Uniquement pour la stratégie `exec` (F2.9). |
| Journalisation | `log/slog` (bibliothèque standard) | JSON structuré, aucune dépendance (N5). |
| Interface web | `embed` (standard) + HTMX servi localement | HTMX est embarqué dans le binaire, jamais chargé depuis un CDN (F11.1). |
| Tests d'intégration | `github.com/testcontainers/testcontainers-go` | Dépendance de test uniquement ; n'affecte pas le binaire. |

---

## Colophon

Keeper — cahier des charges MVP · version 0.2, brouillon soumis à revue.
Établi à partir de l'analyse des dépôts Portabase (serveur Next.js, agent Rust, CLI Python), septembre 2026.
Les identifiants d'exigence (`F1.1`, `N3`, `L2`…) sont stables et destinés à être cités dans les tickets, les commits et les tests.

Version présentable de ce document (HTML mis en page) : https://claude.ai/artifact/LExEbnjzyWxdXUcC8wW4wG
