# ARCHITECTURE — koffr

Vue d'ensemble de la structure et des règles qui la tiennent. Le pourquoi est dans `docs/adr/`.
Rempli au lot 0 après les ADR de cadrage ; relu à chaque rétro. Ce fichier décrit **ce qui est**, pas
ce qu'on aimerait : une règle écrite ici est vérifiée par le lint ou par `internal/arch`, ou elle
n'y est pas.

## Vue d'ensemble

koffr est un binaire unique. Il n'a pas de requête entrante à servir au lot 0 : ce qui le traverse
est une **commande** (CLI) ou une **échéance** (planificateur, à partir du lot 5). Les deux
appellent un cas d'usage du domaine, qui ne connaît ni SQLite, ni le réseau, ni les sous-process.

```
commande CLI ─┐
              ├─► internal/cli ─► domaine (internal/domain/<module>) ─┬─► ports ─► engine / store / pipeline
échéance ─────┘                                                      ├─► ports ─► state (SQLite)
interface web ─► internal/httpd ────────────────────────────────────┘        └─► ports ─► egress (SMTP, webhook, uplink)
```

Le domaine déclare les **ports** dont il a besoin ; les adaptateurs les implémentent ; `cmd/koffr`
les câble. C'est ce qui rend le métier testable sans base, sans réseau et sans outil externe.

## Arborescence

Telle qu'elle est, au lot 0. Les paquets du domaine existent avec leur `doc.go` et se remplissent
au fil des lots.

```
cmd/koffr/            # point d'entrée : câblage et racine cobra
protocol/             # paquet PUBLIC : messages de liaison. N'importe rien du dépôt (ADR-0001)
internal/
  build/              # ce qu'est ce binaire : version, commit, date, plateforme (-ldflags)
  cli/                # commandes cobra : version, config validate, config show
  config/             # CFG — analyse stricte du YAML, chemins de E-026, seul lecteur de l'environnement
  domain/             # métier pur : aucun import d'adaptateur, de réseau, de sous-process
    resolve/          # RSV — résolution des outils, matrice de compatibilité
    backup/           # BKP — sauvegarde, tampon, manifeste
    verify/           # VRF — empreinte et structure
    catalog/          # CAT — index des archives et des emplacements
    retention/        # RET — politique GFS et règles de sûreté
    restore/          # RST — contrôles préalables et modes de nettoyage
    schedule/         # SCH — échéances, rattrapage, plancher opposable
    alert/            # ALR — événements, anti-répétition, délégation
    crypto/           # CRY — destinataires age, règles de clé
    shared/           # erreurs typées, horodatage, tailles, contexte d'appel
  engine/             # adaptateurs postgresql, mysql, mariadb. Seul paquet autorisé à os/exec
  store/              # adaptateurs filesystem, s3, sftp (interface unique E-066)
  pipeline/           # zstd, age, empreinte, fichier tampon
  state/              # SQLite : migrations embarquées, requêtes. Seul paquet autorisé à database/sql
  egress/             # porte de sortie : SMTP, webhook, uplink, téléchargement d'outils (ADR-0008)
  obs/                # journaux slog JSON, rotation interne, masquage des attributs sensibles
  httpd/              # interface web embarquée et API JSON
  arch/               # aucun code : son test lit le graphe réel des imports et fait échouer verify
docs/
scripts/              # spike d'édition de liens, contrôle de publication
```

Trois paquets ne figurent pas dans ADR-0010 et ont été ajoutés au lot 0 : `build` (exigé par
`E-117`, qui n'est pas mesurable sans binaire versionné), `obs` (`E-121`) et `arch` (`N-8`). Aucun
ne porte de règle métier.

## Règles de dépendance (bloquantes)

Les neuf règles d'ADR-0010. Huit sont vérifiées par `internal/arch` sur le **graphe réel des
imports**, avec une fixture violante par règle sous `internal/arch/testdata/` ; la neuvième porte
sur un appel et non sur un import, et c'est `forbidigo` qui la tient.

| # | Depuis | Interdit | Tenue par |
| --- | --- | --- | --- |
| `AR-01` | `internal/domain/**` | `engine`, `store`, `pipeline`, `state`, `egress`, `httpd`, `cli` | `internal/arch` |
| `AR-02` | `internal/domain/**` | `net`, `net/http`, `net/smtp`, `os/exec`, `database/sql` | `internal/arch` |
| `AR-03` | `internal/domain/<a>` | `internal/domain/<b>` ; seul `shared` est commun | `internal/arch` |
| `AR-04` | `internal/httpd/**`, `internal/cli/**` | `internal/state` — passer par un cas d'usage | `internal/arch` |
| `AR-05` | tout sauf `internal/config` | `os.Getenv`, `os.LookupEnv`, `os.Environ` | `forbidigo` |
| `AR-06` | tout sauf `store`, `engine`, `egress` | `net`, `net/smtp` ; `net/http` est en plus ouvert à `httpd`, qui **écoute** et n'appelle pas (`N-14`) | `internal/arch` |
| `AR-07` | tout sauf `internal/engine` | `os/exec` | `internal/arch` **et** `depguard` |
| `AR-08` | tout sauf `internal/state` | `database/sql`, `modernc.org/sqlite` | `internal/arch` |
| `AR-09` | `protocol/` | tout le reste du dépôt | `internal/arch` |

`depguard` interdit par ailleurs `github.com/mattn/go-sqlite3` partout (`E-027`). Cet interdit ne
peut pas être *démontré* tant que le module n'est pas dans `go.mod` : `typecheck` échoue d'abord et
court-circuite les autres linters.

## Modules

Une donnée appartient à un seul module. Le code `MOD` préfixe les règles de son `rules.md`.

Proposés par ADR-0010 (statut **proposé** : rien n'est codé dessus). Les modules du domaine vivent
dans `internal/domain/` ; `engine`, `store`, `pipeline`, `state`, `egress` et `httpd` sont des
adaptateurs et n'ont pas de `rules.md`.

| Module      | Code | Contenu                                                                   | Exigences principales |
| ----------- | ---- | ------------------------------------------------------------------------- | --------------------- |
| `config`    | CFG  | analyse stricte du YAML, formes `*_env` / `*_file`, validation au démarrage | `E-032`…`E-037` |
| `resolve`   | RSV  | énumération des candidats, version exécutée, matrice de compatibilité, installation gérée | `E-038`…`E-050`, `E-130`, `E-131` |
| `backup`    | BKP  | verrou par base, espace disque, format, politique de tampon, manifeste     | `E-024`, `E-025`, `E-029`, `E-030`, `E-051`…`E-061` |
| `verify`    | VRF  | empreinte, relecture de structure, revérification périodique               | `E-008`, `E-062`…`E-065` |
| `catalog`   | CAT  | index des archives et des emplacements, état par destination               | `E-028`, `E-057`, `E-068`, `E-070` |
| `retention` | RET  | politique grand-père/père/fils, règles de sûreté, journal des suppressions | `E-077`…`E-081` |
| `restore`   | RST  | contrôles préalables, modes de nettoyage, cible alternative                | `E-082`…`E-087` |
| `schedule`  | SCH  | échéances persistées, absence de rafale, plancher opposable                | `E-088`…`E-091` |
| `alert`     | ALR  | neuf événements, anti-répétition, rétablissement, délégation et repli      | `E-010`, `E-092`…`E-097` |
| `crypto`    | CRY  | destinataires `age`, séquestre, règles de clé                              | `E-072`…`E-076`, `E-132` |
| `uplink`    | UPL  | poussée d'état, validation stricte des réponses, file locale               | `E-017`, `E-105`…`E-111` |

Le reste de ce fichier (vue d'ensemble, arborescence, flux, auth, jobs, intégrations, déploiement)
se remplit au **lot 0**, une fois les ADR acceptés et la première traversée de couches écrite.

## Flux d'une sauvegarde

Ce que le lot 2 mettra en place ; la structure est posée pour l'accueillir.

1. Une commande ou une échéance construit le contexte d'appel et appelle le cas d'usage
   `domain/backup`.
2. Le cas d'usage prend le **verrou de la base** (`E-051`, un job actif par base), vérifie l'espace
   disque, et demande à `resolve` quel outil convient — par un port, jamais par un import.
3. `engine` lance le dump en **sous-process** ; `pipeline` enchaîne compression, chiffrement et
   empreinte au vol ; `store` écrit sur chaque destination.
4. `catalog` enregistre l'archive et ses emplacements dans `state` ; le **manifeste** est déposé à
   côté de l'archive, non chiffré, et fait foi — le catalogue n'est qu'un index (ADR-0006).
5. Les erreurs métier typées remontent ; la commande les traduit, et `alert` en tire un événement.

## Auth et droits

Il n'y a **pas** d'utilisateur au sens d'un compte : ADR-0009 pose que l'interface web locale
n'écoute que sur `127.0.0.1` et n'a pas d'authentification dans le MVP, et que l'accès est celui de
la machine. `Q-10` reste ouverte pour une écoute hors boucle locale. Le seul contrôle d'autorisation
du produit est **`allow_restore`**, armé par base dans la configuration (`Q-13`), vérifié dans
`domain/restore`, jamais dans la couche transport.

## Jobs et traitements différés

Le planificateur est **interne** (`robfig/cron/v3`, ADR-0002), dans le fuseau déclaré (`E-036`), sans
file de messages : `E-119` interdit d'exiger un service tiers. Les échéances sont persistées dans
`schedules`, ce qui permet de constater un rendez-vous manqué au redémarrage plutôt que de rejouer
une rafale (`E-088`…`E-091`, lot 5). Un job actif par base, par verrou applicatif.

## Intégrations

Deux régimes, fixés par ADR-0008 et visibles dans l'arborescence :

- les **sorties fonctionnelles** — `store/` et `engine/` — écrivent réellement, y compris en
  développement : c'est la fonction du produit ;
- les **sorties d'exploitation** — SMTP, webhook, liaison, téléchargement d'outils — passent par
  `internal/egress`, dont le mode par défaut est le **puits** : il journalise et n'envoie pas. Une
  configuration absente met l'intégration en puits, jamais en erreur, jamais vers une valeur réelle.
  `Gate.Dial` est le seul endroit du dépôt qui ouvre une connexion d'exploitation.

## Tests

- **Domaine** : un test par règle de `rules.md`, écrit avant le code.
- **Adaptateurs** : `state` sur une base SQLite réelle (jamais simulée) ; `engine` et `store`
  contre des conteneurs éphémères à partir du lot 2 (`E-123`, ADR-0008).
- **Architecture** : `internal/arch` lit le graphe réel des imports ; ses fixtures violantes
  doivent faire échouer le contrôle, sans quoi le contrôle ne vaut rien.
- **Publication** : `mise run release` construit les trois cibles, refuse un paquet exigeant cgo,
  refuse un ELF dynamiquement lié et refuse un binaire au-dessus de 30 Mo (`E-117`).
- **Parcours** : les onze scénarios du § 8 du CDC, joués au lot final.

## Déploiement

- Binaire statique pour `linux/amd64`, `linux/arm64` et `darwin/arm64` (ADR-0011 ; Windows hors
  périmètre). Sur Linux, aucun interpréteur ELF : il s'exécute sur un hôte sans libc, musl compris.
  Sur macOS, Go passe toujours par `libSystem` : « statique » y veut dire « sans cgo » (`N-17`).
- Arborescence de `E-026` : `/etc/koffr/`, `/var/lib/koffr/` (base, `tools/`, `tmp/` purgé à
  l'ouverture de l'état), `/var/log/koffr/`. `--config` et `--state-dir` la déplacent.
- Unité `systemd` durcie et image conteneur : lot 7. Aucun service tiers requis (`E-119`) : pas de
  base externe, pas de file de messages, pas de `logrotate`.
