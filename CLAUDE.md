# koffr — CLAUDE.md

koffr est un agent de sauvegarde de bases de données autonome — un binaire Go statique, sans Docker
et sans serveur central obligatoire — pour l'exploitant d'un parc PostgreSQL, MySQL et MariaDB
auto-hébergé. Ce fichier est volontairement
court : il dit où lire, quoi lancer, et les conventions qu'on ne peut pas déduire du code. Le
reste vit dans les documents pointés ci-dessous.

## À lire avant d'agir

- **`ROADMAP.md` en début de chaque tâche** : quel lot est en cours, son critère de sortie, son plan.
- `METHODE.md` : comment on travaille (cycle, registres, exécution des plans, Definition of done).
- @ARCHITECTURE.md : structure, règles de dépendance, flux d'une requête.
- `docs/adr/` : décisions figées. On en change par un nouvel ADR, jamais en contournant dans le code.
- `docs/cdc/` : le cahier des charges et le registre d'exigences `E-nn`. C'est la spécification
  fonctionnelle ; ce qu'il ne dit pas se demande (`docs/questions.md`), ne s'invente pas.
- `docs/README.md` : index de la documentation et table des registres.
- `docs/cdc/KEEPER-CDC.md` : le cahier des charges reçu, **jamais modifié**. Le produit s'y appelle
  encore « Keeper » et ses commandes `keeper …` : lire `koffr` partout (ADR-0001).

## TDD : le test avant le code, toujours

Ce projet est développé en **TDD**. Ce n'est pas une préférence de style, c'est la façon dont on
prouve qu'une règle est appliquée.

- **Rouge, vert, refactor.** Pour tout code de domaine, de service, de job ou d'intégration : on
  écrit le test, on le lance, on **constate le rouge**, puis on écrit le code minimal qui le fait
  passer, puis on refactore, le test restant vert. Un test écrit après le code n'est pas du TDD.
- **Une règle métier = une ligne dans `rules.md` = un test nommé.** La ligne cite sa source
  (`E-nn`, `Q-nn`, ADR) ; le test porte le numéro de la règle dans son nom.
- **Le test dit ce que le code doit faire, pas ce qu'il fait.** Il se lit comme la règle. Cas
  nominal, cas limite, cas d'erreur typée : les trois, ou on dit lequel manque et pourquoi.
- **Là où un test est impossible ou disproportionné** (interface pure, configuration, script
  unique), on le **dit** dans la tâche du plan au lieu de forcer un test sans valeur ; un écran a
  au minimum son test de composant ou son parcours.
- **On ne coche pas une tâche dont le test n'a pas tourné**, et on ne merge pas une vague sans
  `verify` vert.

Le mode opératoire détaillé (fixtures, base réelle, nommage) est dans le skill `implementer`.

## Commandes

Toutes les tâches passent par `mise` (`N-3` du plan du lot 0) ; il n'y a pas de `Makefile`.

```sh
mise install                 # Go et golangci-lint aux versions de mise.toml
mise run check               # go vet + go build (bloquant)
mise run lint                # golangci-lint : format, lint et interdits globaux (bloquant)
mise run fmt                 # applique les formateurs (gofumpt, goimports)
mise run test                # go test ./... -race
mise run build               # binaire statique CGO_ENABLED=0 dans dist/koffr
mise run verify              # les quatre ci-dessus, dans l'ordre ; vert avant tout commit sur main
```

Versions des outils : `mise.toml` fait autorité (Go 1.27, `golangci-lint` 2.13.2), y compris en CI.
Dépendances pinnées dans `go.mod` ; mise à jour hebdomadaire planifiée dans `docs/maintenance.md`.

## Conventions de la stack (ce que le modèle ne sait pas ou sait faux)

{{À remplir au lot 0, depuis l'ADR de stack et le spike. Une puce par piège réellement rencontré :
API qui a changé de nom, fichier de configuration qui n'existe plus, avertissement connu à ne pas
« corriger ». Pas de rappel de ce que la documentation officielle dit déjà et que le modèle sait.}}

## Règles d'architecture (vérifiées par le lint, voir ARCHITECTURE.md)

{{À remplir au lot 0. Exemples de la forme attendue :}}

- `src/lib/domain/` n'importe rien du framework, ni de `server/`, ni de `routes/`.
- Une route n'appelle jamais la base : elle appelle un service.
- Toute mutation passe par un cas d'usage du domaine, qui vérifie les droits. Pas de contrôle de
  droits dans les routes.
- Chaque module a un `rules.md` : règle, source (`E-nn`, `Q-nn` ou ADR), test.

## Données

{{À remplir au lot 0. Ce qui est fixé par ADR : types des montants, des dates, des statuts ;
colonnes d'audit ; suppression logique ou non ; où vivent les agrégats.}}

## Écritures externes

- **Aucun hôte, jeton, clé ou destinataire dans le code ni en base.** Tout vient de la
  configuration d'environnement validée au démarrage. Une variable manquante met l'intégration en
  mode « puits » (journalise, n'envoie pas), jamais en erreur, jamais vers une valeur réelle.
- **Tout appel sortant d'exploitation** (SMTP, webhook, liaison, téléchargement d'outils) passe par
  `internal/egress`. Les **destinations** (`store/`) et les **moteurs** (`engine/`) écrivent
  réellement : c'est la fonction du produit, voir ADR-0008. Le domaine ne connaît
  aucun client réseau.
- Hors production, la porte journalise et **n'envoie pas**. Les mails vont dans un collecteur local.
  On ne teste pas contre un tiers réel depuis un poste de développement.

## Interface

- Langue des utilisateurs : **anglais** (ADR-0003). Pas de phrases d'explication à l'écran : des champs,
  des valeurs, des libellés courts, et des messages au moment de l'action. Le pourquoi vit dans
  `rules.md`.
- Une idée de modernisation ou une fonctionnalité hors cahier des charges va dans
  `docs/backlog.md`, jamais dans le code avant décision.

## Langues et nommage

- Documentation, ADR, roadmap, plans, registres, skills, `rules.md`, ce fichier : **français**.
- Code, identifiants, commentaires, noms de fichiers de code, **noms de branches**, **messages de
  commit**, tables et colonnes : **anglais**.
- Textes d'interface, messages d'erreur affichés, journaux, e-mails : **anglais** (ADR-0003).

### Glossaire métier FR → EN

Un terme métier, un identifiant, pour toujours. Alimenté par `/demarrer-projet` depuis le cahier
des charges, complété au fil des lots.

Le CDC **fixe déjà** la plupart des identifiants : clés de configuration (§ 5.1), noms de composants
(§ 4.2), tables (§ 4.4), champs du manifeste (§ 5.3), noms d'événements (§ 5.10), commandes (§ 5.12).
Ceux-là ne se renégocient pas. La colonne « Notes » signale ce qui est **proposé** par nous, donc
encore modifiable, et ce qui est **en question**.

Les commandes sont écrites ici telles que le CDC les nomme (`keeper …`). ADR-0001 **propose** de
renommer le produit en `koffr` (agent) et `koffr-server` (serveur central) : dès qu'il est accepté,
lire `koffr …` partout, ainsi que `/etc/koffr/`, `/var/lib/koffr/` et `koffr.service`.

| Français | Identifiant | Notes |
| -------- | ----------- | ----- |
| agent | `agent` | l'installation sur une machine ; `agent.id` |
| base (de données à sauvegarder) | `database` | jamais `db` ; table `databases`, clé `database_id` |
| moteur | `engine` | valeurs `postgresql`, `mysql`, `mariadb` |
| famille (MySQL / MariaDB) | `family` | proposé ; `F2.4` détecte la famille à la connexion |
| outil (de dump ou de restauration) | `tool` | binaire externe ; commandes `keeper tools …` |
| résolution, résolveur | `resolver` | composant § 4.2 |
| provenance d'un outil | `source` | valeurs `host`, `managed`, `container` (manifeste § 5.3) |
| stratégie de dump | `strategy` | valeur `exec` pour le dump via conteneur |
| version majeure | `major` | la matrice de compatibilité raisonne en majeures |
| sonde | `probe` | proposé ; joignabilité, version, famille, détection MyISAM |
| job, exécution | `job` | table `jobs` |
| ligne de journal de job | `job_log` | table `job_logs` |
| sauvegarde, archive | `backup` | table `backups`, identifiant `backup_id` |
| emplacement (archive × destination) | `backup_location` | table `backup_locations` |
| manifeste | `manifest` | JSON non chiffré déposé à côté de l'archive |
| empreinte | `checksum` | champs `sha256_raw`, `sha256_stored` |
| chaîne de traitement | `pipeline` | composant § 4.2 ; champ `pipeline` du manifeste |
| mise en tampon | `staging` | clé `staging`, modes `stage`, `stream`, `auto` |
| compression | `compression` | zstd ; niveau noté `zstd:3` dans le manifeste |
| chiffrement | `encryption` | section `encryption` |
| destinataire (clé publique `age`) | `recipient` | `recipients_file`, `recipients.txt` |
| clé de séquestre | `escrow key` | proposé ; second destinataire obligatoire (`E-132`) |
| destination | `destination` | types `filesystem`, `s3`, `sftp` |
| abstraction de destination | `store` | composant § 4.2 |
| catalogue | `catalog` | composant § 4.2 |
| vérification | `verify` | champ `verified` : `checksum`, `structure`, `at` |
| rétention | `retention` | règles `last`, `daily`, `weekly`, `monthly` |
| restauration | `restore` | commande `keeper restore` |
| mode de nettoyage | `clean-mode` | `none`, `clean`, `drop-schemas`, `drop-database` |
| armement de la restauration | `allow_restore` | défaut `false` (en question, `Q-13`) |
| planning | `schedule` | table `schedules`, clé `schedule` (cron 5 champs) |
| planificateur | `scheduler` | composant § 4.2 |
| plancher de fréquence | `min_interval` | **nom en question** (`Q-05`) : sémantique inverse de son nom |
| décalage aléatoire | `jitter` | proposé ; `F9.3` |
| alerte | `alert` | table `alerts` |
| événement | `event` | noms fixés : `backup_missed`, `backup_failed`, `verify_failed`, `database_unreachable`, `destination_failed`, `tool_missing`, `retention_blocked`, `uplink_lost`, `remote_command_rejected` |
| canal d'alerte | `channel` | section `alerts.channels` |
| règle d'alerte | `rule` | section `alerts.rules` |
| anti-répétition | `suppression` | proposé ; délai de rappel `reminder_after` (proposé) |
| rétablissement | `recovered` | proposé ; ne jamais écrire `recovery`, qui dit « restauration » |
| liaison au serveur central | `uplink` | composant § 4.2 ; section `server` |
| serveur central | `server` | jamais `master`, jamais `hub` |
| parc | `fleet` | proposé ; le mot « parc » est du CDC, l'identifiant est à nous |
| jeton de liaison | `token` | `token_file` |
| interface web locale | `httpd` (composant), `ui` (commande) | deux mots pour deux choses |
| diagnostic | `doctor` | commande `keeper doctor` |
| configuration | `config` | `keeper.yaml` |
| fuseau horaire | `timezone` | clé `agent.timezone` |
| délai de grâce | `grace_period` | proposé ; `F3.9` |
| verrou de base | `lock` | `F3.1`, un job actif par base |

## Definition of done

Celle de `METHODE.md` § « Definition of done d'un lot ». En résumé, une tâche est finie quand :
`verify` passe ; chaque règle métier a un test **écrit avant le code** et une ligne dans `rules.md` ; les frontières
d'architecture tiennent ; toute mutation vérifie les droits ; toute évolution de schéma a sa
migration relue ; code en anglais, pilotage en français ; la roadmap et le plan sont à jour ; un
ADR existe pour toute décision structurante.

## Git

- Commits conventionnels **en anglais** : `feat(<module>): …`, `fix(<module>): …`, `chore: …`,
  `docs: …`. Le sujet dit ce que le commit change, pas ce qu'on a fait pour y arriver.
- Branches **en anglais** : `lot<N>/wave-<n>-<slug>` pour une vague de plan, `fix/<slug>`,
  `chore/<slug>`. `main` toujours vert ; merge `--no-ff` d'une vague vérifiée.
- Ne jamais commiter `.env`, `CLAUDE.local.md`, ni un document reçu marqué confidentiel.
