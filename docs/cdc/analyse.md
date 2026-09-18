# Analyse du cahier des charges — Keeper

Ce que nous lisons dans `KEEPER-CDC.md`, avant toute décision. Ce document n'interprète pas : il
classe, cite et signale. Tout ce que le CDC ne dit pas est une `Q-nn` ; tout arbitrage qu'il laisse
ouvert est une `D-nn`. Le registre des exigences est dans `exigences.md`.

Produit le 2026-09-18 par `/demarrer-projet`. À relire par le propriétaire (arrêt 1).

---

## 1. Fiche du document

| Champ | Valeur |
| ----- | ------ |
| Titre | Keeper — Cahier des charges MVP |
| Version | 0.2, **brouillon soumis à revue** |
| Date | 18 septembre 2026 (soit le jour de cette analyse) |
| Auteur | non nommé. Le colophon indique que le document est « établi à partir de l'analyse des dépôts Portabase (serveur Next.js, agent Rust, CLI Python), septembre 2026 » |
| Destinataire explicite | l'agent d'implémentation : le § « Instructions pour l'agent d'implémentation » s'adresse directement à lui |
| Format | Markdown, **sans pagination** → les sources du registre sont des § et des identifiants (`F2.3`, `N7`), jamais des numéros de page |
| Référence externe | version HTML mise en page : https://claude.ai/artifact/LExEbnjzyWxdXUcC8wW4wG (non lue ; supposée identique) |

**Ce qu'il couvre, selon ses propres termes** : la spécification fonctionnelle et non fonctionnelle
d'un MVP d'agent de sauvegarde, son modèle de menace, ses critères d'acceptation, un découpage en
jalons `L0`–`L5` et une pile technique indicative (§ 12, « à confirmer au moment d'écrire le
`go.mod` »).

**Ce qu'il ne couvre pas, selon ses propres termes** :

- le **serveur central lui-même** : « fait l'objet d'un cahier des charges distinct » (§ 9, L5) ;
  seul son protocole, côté agent, est dans le MVP (§ 3) ;
- les non-objectifs du § 1.3 et les écarts du § 10 (dumpers natifs, PITR, RBAC, déduplication…) ;
- quatre décisions que le document renvoie explicitement au propriétaire (§ 11.1) : nom, licence,
  déduplication, portée de Windows, calendrier du CDC du serveur central.

**Statut à retenir** : c'est un **brouillon soumis à revue**, écrit avant la première ligne de code,
et qui le dit (instruction 7 : « si une exigence te paraît fausse ou incohérente, dis-le »). Nous le
traitons comme une source de qualité, pas comme une vérité exécutable — ce qui est exactement la
règle de `METHODE.md`.

---

## 2. Périmètre en une page

**À quoi sert le produit.** Un démon Go distribué en un seul binaire statique, installé sur une
machine, qui sauvegarde des bases de données selon un planning : il résout l'outil de dump adapté,
dumpe en flux, compresse (zstd), chiffre avec une clé publique (`age`), écrit sur une ou plusieurs
destinations, vérifie que l'archive est relisible, applique une politique de rétention, alerte quand
quelque chose ne va pas, et restaure sur demande locale. Il expose une interface web locale en
lecture et action, et une CLI complète.

**Pour qui.** L'exploitant d'un parc de bases de données auto-hébergées : administrateur système ou
équipe d'exploitation qui gère de quelques bases à quelques dizaines, sur des serveurs où Docker
n'est pas toujours autorisé.

**Ce qu'il remplace.** Les outils de sauvegarde auto-hébergés existants qui, selon le § 1.1,
(a) centralisent les identifiants dans un serveur d'orchestration dont la compromission donne accès
à toute la production, et (b) imposent Docker pour embarquer la matrice des versions d'outils de
dump. Le document est écrit à partir de l'analyse de **Portabase** (§ colophon).

**Les deux paris du produit.**

1. *Technique* : ne jamais réimplémenter `pg_dump`/`mysqldump`, mais garantir qu'on appelle toujours
   **la bonne version**, vérifiée en l'exécutant — et refuser de sauvegarder plutôt que de produire
   une archive douteuse (`P3`, `F2`).
2. *Sécurité* : le serveur central est une commodité, pas un organe vital. Il ne détient rien et ne
   peut rien ordonner de destructif (`P1`, `P2`, § 6).

**Ce qui n'est pas dans le MVP mais contraint dès maintenant.** Le protocole du serveur central. Le
§ 3 en donne la raison, qui est une décision d'architecture et non de calendrier : « un protocole
ajouté après coup contamine toujours le modèle de l'agent […] En le définissant d'abord, la
frontière de confiance est posée avant qu'on puisse la contourner par facilité. »

---

## 3. Acteurs et rôles

Le CDC ne décrit pas d'acteurs sous cette forme : ce n'est pas un logiciel de gestion, il n'a pas
d'utilisateurs métier. Nous reconstituons les rôles depuis les surfaces d'accès, parce qu'ils sont le
socle du modèle de droits.

| Acteur | Ce qu'il fait | Par quelle surface | Combien |
| ------ | ------------- | ------------------ | ------- |
| **Opérateur** (administrateur de la machine) | installe, écrit la configuration, déclenche une sauvegarde, vérifie, restaure, applique la rétention, installe des outils | CLI locale + interface web sur `127.0.0.1` | non dit ; supposé 1 à quelques-uns |
| **Détenteur de la clé privée** | conserve la clé `age` hors machine, déchiffre manuellement lors d'une restauration | outil `age` standard, hors Keeper (`F6.3`, `F6.4`) | non dit ; au moins 2 destinataires exigés (`F6.2`) |
| **Destinataire d'alerte** (équipe d'exploitation) | reçoit les événements par e-mail ou webhook | SMTP, webhook JSON | non dit |
| **Serveur central** (système, hors MVP) | lit l'état du parc, propose une expression cron, émet les notifications déléguées | HTTPS entrant côté serveur, poussé par l'agent | 0 ou 1 par parc |
| **Base de données** (système) | est sondée, dumpée, restaurée | pilote SQL + sous-process de dump | 1..n par agent |
| **Destination** (système) | reçoit, restitue, liste, supprime des archives | disque, S3, SFTP | 1..n par base |

**Conséquence pour le modèle de droits — et c'est un point à trancher.** Le § 3 met
« authentification multi-utilisateur, RBAC » **hors MVP** et le § 10 le justifie : « l'interface
locale est déjà protégée par l'accès à la machine ». Le modèle de droits du MVP n'est donc pas
`module:action` avec des rôles, comme le suppose le kit, mais **binaire** : qui a accès à la machine
a tous les droits locaux ; le seul acteur à droits restreints est le serveur central, dont le pouvoir
est décrit en creux par le § 6.1 (une seule écriture autorisée, cinq classes d'écritures refusées).
C'est un écart assumé au gabarit du kit (`CLAUDE.md` § Definition of done, point 4), à inscrire dans
un ADR plutôt qu'à contourner. Il reste une inconnue : `F11.2` exige « une authentification
configurée » dès que l'interface écoute ailleurs que sur `127.0.0.1`, sans dire laquelle (`Q-10`).

---

## 4. Objets métier

Un objet = un nom qui revient, sa définition selon le CDC, ses états, ses relations. Ces objets
fondent les modules et le glossaire de `CLAUDE.md`.

| Objet (FR) | Définition selon le CDC | États | Relations |
| ---------- | ----------------------- | ----- | --------- |
| **Agent** | le démon installé sur une machine ; porte un `id` et un fuseau (§ 5.1) | en service / arrêté ; lié ou non au serveur | 1 agent → n bases, n destinations, n canaux |
| **Base** (`databases`) | une base à sauvegarder : moteur, hôte, port, nom, identifiants, planning, rétention, destinations (§ 5.1) | joignable / injoignable (`database_unreachable`) ; restauration armée ou non (`allow_restore`) | → n archives, 1 planning, n destinations |
| **Moteur** (`engine`) | l'implémentation par famille : sonde, dump, restauration, nettoyage (§ 4.2) | — | postgresql, mysql, mariadb |
| **Outil** (`tool`) | un binaire de dump ou de restauration candidat, avec sa version **exécutée** et sa provenance (§ 5.2) | candidat / retenu / incompatible ; provenance : hôte, géré, conteneur | n outils → 1 base par opération |
| **Job** | une exécution : type, base, statut, horodatage, durée, code de sortie (§ 4.4) | en cours / réussi / échoué (jamais « réussi » après interruption, `F3.9`) | 1 job → n lignes de journal, 0..1 archive |
| **Archive** / sauvegarde (`backups`) | le produit d'un job : identifiant, base, manifeste, empreintes, état de vérification (§ 4.4) | écrite / vérifiée / non vérifiée / supprimée | 1 archive → n emplacements, 1 manifeste |
| **Emplacement** (`backup_locations`) | un couple archive × destination, avec chemin distant et taille (§ 4.4) | reçu / échoué / vérifié | — |
| **Manifeste** | JSON non chiffré, sans secret, décrivant ce qui a produit l'archive (§ 5.3) | — | stocké à côté de l'archive sur **chaque** destination |
| **Destination** (`destinations`) | un dépôt : `filesystem`, `s3`, `sftp`, derrière une interface unique (§ 5.5) | accessible / en échec | n destinations ↔ n bases |
| **Planning** (`schedules`) | expression cron effective, origine (locale ou serveur), dernier et prochain déclenchement (§ 4.4) | — | 1 par base |
| **Rétention** | politique grand-père/père/fils : `last`, `daily`, `weekly`, `monthly` (§ 5.7) | appliquée / suspendue (`retention_blocked`) | 1 par base |
| **Alerte / événement** (`alerts`) | un des neuf événements du § 5.10, avec sa gravité | émis / rappelé / rétabli | → n canaux par règle |
| **Canal** (`channels`) | e-mail SMTP ou webhook JSON (§ 5.10) | — | — |
| **Destinataire** (`recipients`) | une clé publique `age` X25519 autorisée à déchiffrer (§ 5.6) | opérationnelle / séquestre | n par base (contredit par la configuration, `Q-04`) |
| **Liaison** (`uplink`) | la poussée d'état vers le serveur central (§ 5.13) | active / perdue (`uplink_lost`) | 0..1 par agent |

**Découpage en modules pressenti** (à proposer en ADR à l'étape 2, pas ici) : le § 4.2 donne déjà
neuf composants — `scheduler`, `resolver`, `engine`, `pipeline`, `store`, `catalog`, `notifier`,
`uplink`, `httpd` — plus la configuration et la CLI. C'est un découpage par composant technique, pas
par objet métier : il faudra dire lequel des deux fait autorité pour les frontières de dépendance.

---

## 5. Flux et intégrations

Chaque ligne marquée **sortant** écrit hors du système et devra passer par la porte de sortie unique
exigée par `CLAUDE.md`.

| # | Système tiers | Sens | Format / protocole | Déclencheur | Fréquence |
| - | ------------- | ---- | ------------------ | ----------- | --------- |
| 1 | PostgreSQL 12–18 | entrant (lecture) | `pgx/v5` pour la sonde et la version ; sous-process `pg_dump` pour le dump | planning, `keeper backup`, `doctor` | par planning |
| 2 | MySQL 8.x / MariaDB 10.6+ | entrant (lecture) | `go-sql-driver/mysql` pour la sonde et la **famille** ; `mysqldump`/`mariadb-dump` | idem | idem |
| 3 | Restauration vers une base | **sortant, destructif** | `pg_restore` / client mysql, modes `none`…`drop-database` | action locale uniquement (`F8.1`) | à la demande |
| 4 | Socket Docker | bidirectionnel local | `docker/docker/client`, stratégie `exec` par base | dump d'une base conteneurisée | par job |
| 5 | Destination `filesystem` | **sortant** | écriture de fichier | job, rétention | par job |
| 6 | Destination S3 (MinIO, Scaleway, OVH, Backblaze, AWS) | **sortant** | `aws-sdk-go-v2`, téléversement en parties avec reprise | job, vérification, rétention | par job |
| 7 | Destination SFTP | **sortant** | `pkg/sftp` + `x/crypto/ssh`, clé privée fichier | job, vérification, rétention | par job |
| 8 | SMTP | **sortant** | e-mail, authentifié | règle d'alerte | par événement, avec anti-répétition |
| 9 | Webhook | **sortant** | HTTP POST JSON générique | règle d'alerte | idem |
| 10 | Serveur central | **sortant** | HTTPS **poussé par l'agent** ; jeton fichier, mTLS optionnel | périodique (fréquence non dite, `Q-16`) | non dit |
| 11 | Dépôt d'archives d'outils | **sortant** (téléchargement) | HTTPS, SHA-256 épinglée + signature (`F2.7`) | `keeper tools install` | rare |
| 12 | `pg_lsclusters`, chemins système | local | exécution de binaires | résolution | par job, avec cache |

**Ce que cela implique pour la règle « écritures externes » du kit.** `CLAUDE.md` impose qu'aucune
écriture externe ne parte d'un poste de développement, et qu'une intégration sans configuration passe
en mode « puits ». Pour Keeper, cette règle ne peut pas s'appliquer telle quelle : **écrire sur une
destination est la fonction du produit**, pas un effet de bord, et `N7` exige des tests d'intégration
réels contre des conteneurs éphémères. La règle doit être adaptée par ADR — a priori : mode « puits »
obligatoire pour les canaux d'alerte (8, 9) et la liaison (10) hors production, destinations (5, 6,
7) testées contre des conteneurs locaux et jamais contre un service tiers réel. C'est un écart à
acter, pas à contourner.

---

## 6. Contraintes

| Nature | Ce que dit le CDC | § |
| ------ | ----------------- | - |
| **Langage** | Go 1.23+ | en-tête |
| **Compilation** | `CGO_ENABLED=0`, binaire statique, < 30 Mo, 4 cibles (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`) | `N1` |
| **Dépendances de service** | aucune : pas de base externe, pas de file, pas de runtime. État en SQLite pur Go | `P5`, `N3`, § 12 |
| **Mémoire** | < 50 Mo au repos, indépendante de la taille des bases | `N2` |
| **Hébergement** | machine hôte, service `systemd` durci ; Docker optionnel, jamais prérequis | `N4`, `N8`, § 1.2 |
| **Bibliothèques** | liste indicative § 12 ; un interdit ferme : **ne pas utiliser `mattn/go-sqlite3`** (impose CGO, casse `N1`) | § 12 |
| **Tests** | intégration réelle PostgreSQL 13–18, MariaDB 10.6/10.11/11.4, conteneurs éphémères, aller-retour sauvegarde→restauration systématique | `N7` |
| **Sécurité** | le § 6 est un **contrat**, non négociable au même titre que les six principes | instruction 3, § 2, § 6 |
| **Délai / budget** | **aucune date, aucun budget** dans le document. Rien à opposer à la roadmap | — |
| **Réglementaire** | rien | — |
| **Volumes** | une base de référence de 40 Go (§ 4.5, scénario 11), 5 Go pour la restauration (scénario 5), « quarante agents » évoqués en `F9.3`. Pas de volumétrie de parc engageante | § 4.5, § 8 |
| **Langues** | **rien**. Le CDC est en français, la configuration et les événements sont en anglais. La langue de la CLI, de l'interface et des messages n'est jamais fixée | `Q-17` |

**Écart constaté avec le dépôt.** Le CDC demande Go 1.23+ ; `mise.toml`, déjà présent, déclare
`go = "1.27"`. Ce n'est pas une contradiction (1.27 satisfait « 1.23+ ») mais c'est un choix déjà
posé dans le dépôt sans trace de décision → `D-07`.

---

## 7. Silences — ce qu'un développeur devra savoir et que le CDC ne dit pas

Chaque silence est une `Q-nn` dans `docs/questions.md`. Les cinq premiers bloquent une conception,
pas seulement une valeur par défaut.

| # | Silence | Pourquoi ça bloque | Q |
| - | ------- | ------------------ | - |
| 1 | **Comment `keeper restore` déchiffre-t-il ?** `F6.3` dit que la clé privée n'est jamais requise par l'agent ni présente dans sa configuration, et que « le déchiffrement est une opération manuelle et distincte ». Mais `F8` décrit `keeper restore <db> --from <backup-id>` de bout en bout, `F8.3` contrôle que l'archive est « déchiffrable », et `F11.4` propose l'action depuis l'interface web. Le document ne dit **jamais** par où entre la clé privée. | C'est la conception de toute la commande de restauration, et de son écran | `Q-03` |
| 2 | **Portée du dump.** Une entrée de configuration = une base (`database: boutique`). Rien sur les objets globaux d'un cluster (rôles, tablespaces, extensions), ni sur la sauvegarde de plusieurs bases d'une même instance. L'`argv` du manifeste montre `--no-owner --no-privileges` : propriétaires et droits ne sont donc pas restaurés. | Une restauration « complète » (scénario 5) qui perd les rôles et les droits n'est pas une restauration | `Q-07` |
| 3 | **Identifiants de restauration.** Le § 6.2 recommande « un utilisateur de base dédié, en lecture seule pour la sauvegarde, **distinct de l'utilisateur de restauration** ». La configuration ne porte qu'un `user`/`password_*` par base, et `--into DSN` ne dit pas d'où viennent les identifiants de la cible. | La recommandation de sécurité est inapplicable telle quelle | `Q-06` |
| 4 | **Sur quelle copie porte la vérification.** `F4.1` : empreinte « recalculée à la relecture **de la destination** pour au moins une destination ». Le § 4.5 vend pourtant le mode `stage` en expliquant que vérifier une archive distante « suppose de la retélécharger — de l'egress facturé, chaque nuit ». | Détermine s'il y a de l'egress toutes les nuits, et ce que « vérifiée » signifie pour `F5.3` et `F7.3` | `Q-02` |
| 5 | **Destinataires par base.** `F6.2` exige « plusieurs destinataires **par base** ». La configuration cible ne porte qu'un `encryption.recipients_file` global, sans clé par base. | Structure du fichier de configuration et du chiffrement | `Q-04` |
| 6 | **Sémantique exacte de la rétention** : fuseau des seaux, début de semaine, quelle archive d'un jour compte comme « la » quotidienne, quand la rétention s'exécute, ce qu'on fait quand la destination refuse la suppression (Object Lock, `F5.6`), et si la politique s'applique par destination ou globalement. | Une règle de suppression sans définition exacte est une perte de données | `Q-09` |
| 7 | **Valeurs par défaut absentes** : tolérance de `backup_missed`, `N` sondes de `database_unreachable` et fréquence de la sonde, seuil de bascule vers `-Fd`, délai de grâce `SIGTERM`, plancher d'archives valides de `F7.2` (aucune clé dans la configuration cible), délai de rappel d'alerte, niveau zstd (le manifeste montre `zstd:3`), marge d'espace disque de `F3.10`. | Huit valeurs à inventer, donc huit occasions de trancher seul | `Q-08` |
| 8 | **Signature des archives d'outils** (`F2.7`) : quelle signature, produite par qui, vérifiée avec quelle clé embarquée où. | Chaîne de confiance de l'installation d'outils | `Q-15` |
| 9 | **Protocole de liaison** (`F13`) : fréquence de poussée, format du message, taille et durée de la file locale, provisionnement et rotation du jeton. | Le protocole est dans le MVP par décision de cadrage (§ 3) ; il ne peut pas rester implicite | `Q-16` |
| 10 | **Cron et changement d'heure** : que devient une échéance à 2 h 30 la nuit où cette heure n'existe pas, ou existe deux fois ? `F1.5` impose un fuseau explicite mais ne traite pas le cas. | Un planificateur de sauvegarde nocturne rencontre le cas deux fois par an | `Q-18` |
| 11 | **Changement de configuration** : la table `databases` est « un instantané de la configuration résolue, pour détecter les changements » (§ 4.4) — mais le CDC ne dit jamais ce qu'on **fait** d'un changement détecté (`id` renommé, hôte modifié, base retirée) ni ce que deviennent son catalogue et ses archives. | Détermine le cycle de vie du catalogue | `Q-19` |
| 12 | **Outils gérés hors Linux** : `F2.6` décrit un `RPATH` ajusté et des bibliothèques partagées embarquées — vocabulaire ELF. `N1` cible pourtant `darwin/arm64` et `windows/amd64`. | Lié à la portée de Windows (`D-04`) | `Q-20` |
| 13 | **Croissance de `keeper.db`** : `job_logs` accumule des lignes de journal structurées sans politique de purge. | Un agent qui remplit son disque avec ses propres journaux | `Q-21` |
| 14 | **Reprise de données existantes** : rien sur l'adoption d'un dépôt d'archives préexistant (Portabase ou autre). Le § 1.1 part pourtant d'outils existants. | Migration depuis l'outil remplacé | `Q-22` |
| 15 | **Supervision par métriques** : `N5` couvre les journaux, rien sur un point d'entrée de santé ou des métriques (Prometheus) alors que le produit s'adresse à des exploitants. | Non bloquant → `B-nn` plutôt que `Q-nn` | — |

---

## 8. Contradictions

| # | Ce qui se contredit | Traitement |
| - | ------------------- | ---------- |
| 1 | **Mode de tampon par défaut.** § 4.5 : le mode `stage` est « **Défaut** » et `auto` est la « valeur recommandée en configuration ». `F3.4` : « mode `auto` par défaut ». | `Q-01` |
| 2 | **`min_interval` : nom contre sémantique.** La configuration commente `min_interval: 24h` comme « plancher opposable au serveur central » ; `F9.4` précise qu'« une fréquence proposée **plus lâche** que le plancher est rejetée ». Une fréquence plus lâche que 24 h, c'est un intervalle **plus long** : le champ est donc un intervalle **maximal**, et son nom dit l'inverse. | `Q-05` |
| 3 | **Relecture à chaud.** § 4.3 présente `keeper.yaml` comme « lue à chaud » — un fait. `F1.4` classe la relecture à chaud en **Souhaitable**. | `Q-12` |
| 4 | **Matrice de tests contre périmètre.** § 3 : PostgreSQL 12–18, **MySQL 8.x**, MariaDB 10.6+. `N7` : tests réels contre PostgreSQL **13**–18 et MariaDB seulement. PostgreSQL 12 et **toute la famille MySQL d'Oracle** sont dans le périmètre annoncé et hors de la matrice de tests — alors que `F2.4` fait de la distinction MySQL/MariaDB un point dur. | `Q-14` |
| 5 | **Armement de la restauration.** `F8.6` (`allow_restore: false` par défaut, armement expirant) est **Souhaitable**, mais la configuration cible l'affiche comme un réglage normal et le § 6 en fait un élément du contrat de sécurité. Si `F8.6` est écarté, la restauration est-elle autorisée par défaut ? | `Q-13` |
| 6 | **Technologie de l'interface.** `F11.5` (rendu serveur, HTMX, pas de SPA) est **Souhaitable**, mais `F11.1` (ressources embarquées, aucun accès réseau) et le § 12 (HTMX embarqué, jamais depuis un CDN) supposent ce choix. Écarter `F11.5` n'a pas de repli décrit. | `Q-11` |
| 7 | **Authentification de l'interface.** `F11.2` exige « une authentification configurée » pour écouter hors de `127.0.0.1`, alors que le § 3 met « authentification multi-utilisateur, RBAC » hors MVP et que le § 10 justifie l'absence d'authentification par l'accès à la machine. Quelle authentification, alors ? | `Q-10` |
| 8 | **Quand la liaison doit exister.** § 3 : « l'agent doit pouvoir s'y connecter **dès le premier jour** ». § 9 : la liaison est le **dernier** lot (`L5`). § 8 tempère : le scénario 10 « doit exister dès le premier lot qui active la liaison au serveur ». | `Q-23` |
| 9 | **Windows.** `N1` liste `windows/amd64` comme cible de compilation ; § 11.1 dit que sa portée n'est pas tranchée et exige « pas d'entre-deux ». | `D-04` |
| 10 | **Vérification et succès partiel.** `P4` : « rien n'est sauvegardé tant que ce n'est pas vérifié ». `F5.3` : le job est réussi si **au moins une** destination a reçu et vérifié. `F7.3` : seules les archives vérifiées comptent en rétention. Une archive vérifiée sur une destination et absente d'une autre est donc à la fois « sauvegarde réussie » et incomplètement protégée : le CDC ne dit pas comment le catalogue l'affiche ni ce que compte la rétention **par destination**. | `Q-02`, `Q-09` |

---

## 9. Ce que le CDC dit de ne pas faire

Repris tel quel, avec sa raison. Ces lignes deviennent des exigences de type `hors périmètre` dans le
registre, et alimenteront `docs/backlog.md` à l'étape 3 — elles ne sont pas oubliées, elles attendent.

| Écarté | Raison donnée | § |
| ------ | ------------- | - |
| Réimplémenter `pg_dump`/`mysqldump` en Go | « vingt-cinq ans de correctifs ne se rattrapent pas » ; non-objectif explicite, pas une contrainte de temps (instruction 6) | § 1.3, § 10 |
| Sauvegarde système (volumes, machines, fichiers) | le périmètre est la base de données | § 1.3 |
| Réplication, PITR, streaming WAL, sauvegarde physique | « garanties et complexité d'un autre ordre », relève d'un produit distinct | § 1.3, § 10 |
| Multi-tenant SaaS | le serveur central vise le parc d'une organisation | § 1.3 |
| MongoDB, SQLite, Redis, Valkey, SQL Server, volumes | reporté (le natif est « trivial et sans risque » pour SQLite/Redis/Valkey → à rouvrir) | § 3, § 10 |
| Azure Blob, GCS, Google Drive, rclone | reporté | § 3 |
| `kubectl exec`, dumpers natifs | reporté | § 3 |
| Restauration de test dans une base jetable | « la vérification qui a le plus de valeur », mais suppose de provisionner une instance jetable → v2, en option | § 3, § 10 |
| Slack, Discord, Teams, PagerDuty, SMS natifs | un webhook générique les couvre sans code dédié (`F10.1`) | § 3 |
| Authentification multi-utilisateur, RBAC | l'interface locale est protégée par l'accès à la machine ; les droits appartiennent au serveur central | § 3, § 10 |
| Le serveur central lui-même | lot séparé, cahier des charges distinct | § 3, § 9 |
| Déduplication et sauvegarde incrémentale | « change entièrement le format de stockage. À décider **avant la v1** si l'objectif est de concurrencer les outils à dépôt » → ce n'est pas un écart, c'est une décision à prendre maintenant | § 10, § 11.1 → `D-03` |

---

## 10. Critères d'acceptation et jalons du CDC

### Les onze scénarios du § 8

Ils ne sont **pas** entrés au registre `E-nn` : ce ne sont pas des exigences nouvelles mais la
**recette** des exigences déjà enregistrées. Ils serviront de critères de sortie de lots à l'étape 3
et de scénarios dans `docs/recette/`. Correspondance :

| # | Scénario | Vérifie |
| - | -------- | ------- |
| 1 | Installation et première sauvegarde PostgreSQL 16 en moins de dix minutes | `E-032`, `E-057`, `E-062`, ergonomie d'installation |
| 2 | PostgreSQL 17 avec seulement le client 15 : échec actionnable | `E-042`, `E-047` |
| 3 | `keeper tools install postgresql 17` puis succès sans toucher la configuration | `E-043`, `E-040` |
| 4 | MariaDB conteneurisée, port non publié, stratégie `exec` | `E-046`, `E-041` |
| 5 | Restauration de 5 Go depuis S3, égalité du nombre de lignes par table | `E-084`, `E-086`, `E-069` |
| 6 | Archive illisible sans la clé privée, déchiffrable par `age` standard | `E-072`, `E-075` |
| 7 | Agent arrêté 24 h : un seul `backup_missed`, aucune rafale | `E-089` |
| 8 | Base injoignable : une alerte, un rappel, un rétablissement | `E-092`, `E-094`, `E-095` |
| 9 | Rétention sur trente archives, suspendue si la dernière sauvegarde a échoué | `E-077`, `E-078` |
| 10 | Serveur simulé envoyant un ordre de restauration : rejeté, journalisé, notifié | `E-109`, `E-097`, `E-112` |
| 11 | 40 Go en `stage` : aucun fichier surdimensionné, connexion fermée avant l'envoi, coupure réseau reprise sans relancer le dump | `E-029`, `E-055`, `E-069`, `E-061` |

Le § 8 qualifie le scénario 10 de **critère transversal** : « la vérification que le contrat de
sécurité du document est réellement implémenté, et non seulement écrit ».

### Les jalons `L0`–`L5` du § 9

Le CDC propose son propre découpage : `L0` socle et résolution des outils, `L1` sauvegarde locale de
bout en bout, `L2` restauration et destinations distantes, `L3` automatisation, `L4` interface
locale, `L5` liaison au serveur central. Il est cohérent avec la règle du kit (chaque lot livre
quelque chose de démontrable) et sera la base de la proposition de l'étape 3.

**Une réserve à porter à l'arrêt 3** : le `L0` du CDC (« aucune sauvegarde encore — mais
`keeper doctor` donne déjà un rapport juste ») n'est pas le **lot 0 du kit** (squelette, CI, outillage,
frontières d'architecture, `verify` vert). Ce sont deux choses différentes qui portent le même
numéro, et le § 11 ajoute un préalable qui n'est dans aucun des deux : l'essai réel d'édition de
liens sur Debian, Rocky et Alpine, « à valider **avant L0** », désigné comme « le risque technique
numéro un du document ».

---

## 11. Risques repris du § 11, et ce qu'ils impliquent

| Risque | Gravité (CDC) | Ce que nous en retenons |
| ------ | ------------- | ----------------------- |
| Édition de liens des outils installés (libpq, OpenSSL, zlib, ICU ; glibc différente) | Élevée | Un **spike obligatoire avant tout code de production** (`E-130`), sur trois distributions. C'est le seul endroit du document qui conditionne le premier lot. |
| Distribution des outils : construire et publier 7 versions PostgreSQL × 3 MariaDB × 2 architectures | Moyenne | « Coût récurrent à assumer » : c'est une infrastructure permanente (construction, signature, hébergement, épinglage). Pour un projet à un développeur, c'est un arbitrage, pas un détail → `D-06`. |
| Perte de la clé privée | Élevée | Ajoute deux exigences que le § 5.6 ne porte pas : destinataires multiples **obligatoires dès la configuration initiale**, et avertissement au premier démarrage tant qu'une seule clé est déclarée (`E-132`). |
| Cohérence MyISAM | Moyenne | Détection à la sonde, avertissement dans le manifeste **et** dans l'interface (`E-056`). |
| `-Fd` indisponible en mode `exec` | Faible | Arbitrage documenté, signalé par `doctor` (`E-054`, `E-031`). |
| Limite de 10 000 parties S3 | Moyenne | Taille de partie adaptative, calculée depuis la taille attendue en `stage` et **révisée en cours de route** en `stream` (`E-069`). |

---

## 12. Ce que nous n'avons pas compris

À poser au propriétaire à l'arrêt 1, sans attendre les réponses aux `Q-nn`.

1. **Le chemin de la clé privée dans la restauration** (`Q-03`). Nous ne voyons pas de lecture du
   document qui rende `F6.3` et `F8` simultanément vrais, sauf à supposer que `keeper restore` lit la
   clé depuis une entrée fournie par l'opérateur au moment de la commande. Si c'est l'intention, elle
   n'est écrite nulle part, et elle change l'écran de `F11.4`.
2. **Le statut réel de MySQL (Oracle)** (`Q-14`). Le document le met dans le périmètre, insiste sur
   la non-confusion des familles (`F2.4`), et ne le teste jamais (`N7`). Nous ne savons pas si c'est
   un oubli de la matrice de tests ou un périmètre annoncé plus large que l'intention.
3. **La nature du serveur central dans ce dépôt** : le protocole est dans le MVP, le serveur a son
   propre CDC. Nous ne savons pas si le serveur vivra dans ce dépôt, dans un autre, ni qui l'écrira
   → `D-08`.
4. **Ce que « parc » veut dire en volume**. `F9.3` évoque « quarante agents », le § 4.5 une base de
   40 Go. Nous ne savons pas si l'ordre de grandeur visé est 5 bases sur 2 machines ou 200 bases sur
   40 machines : cela ne change pas le MVP, mais cela changera la conception du serveur central et
   probablement la politique de journalisation.
5. **La place de ce CDC dans la durée**. Il est en version 0.2, « brouillon soumis à revue », et il
   demande lui-même qu'on lui signale ses erreurs. Nous supposons que le propriétaire en est l'auteur
   et peut donc trancher les `Q-nn` directement ; si le rédacteur est quelqu'un d'autre, la colonne
   « Posée à » de `docs/questions.md` est à corriger.
