# Questions au métier — registre `Q-nn`

Ce qu'on ne sait pas et qu'on demande aux utilisateurs, au métier, à la direction ou au rédacteur
du cahier des charges. Une question se pose, se date, et sa réponse revient ici avec la date et ce
qu'on en a fait. On ne supprime pas une question répondue : on la barre dans l'état et on garde la
réponse.

| Ce qui va ici                                                                     | Où va le reste            |
| --------------------------------------------------------------------------------- | ------------------------- |
| « Est-ce voulu ? », « Quel seuil ? », « Qui s'en sert ? », « Que fait-on quand… ? » | —                         |
| Une contradiction ou un silence du cahier des charges                             | — (citer le §)            |
| Un arbitrage de projet (budget, calendrier, technique)                            | `decisions.md`            |
| Une idée d'amélioration                                                           | `backlog.md`              |

Toutes les questions ci-dessous sont posées au **rédacteur du CDC**, supposé être le propriétaire
du projet (`analyse.md` § 12, point 5). Si ce n'est pas le cas, la colonne « Posée à » est à
corriger avant de les envoyer.

## État

| #    | Question (titre) | Posée à | Posée le | Bloque | Répondue le |
| ---- | ---------------- | ------- | -------- | ------ | ----------- |
| ~~Q-01~~ | ~~Quel mode de tampon par défaut : `stage` ou `auto` ?~~ | rédacteur | 2026-09-18 | `E-053`, `E-029` | **2026-09-22 → ADR-0016** : `auto` |
| Q-02 | La vérification relit-elle la destination ou le fichier tampon ? | rédacteur | 2026-09-18 | `E-062`, `E-068`, `E-079` | |
| ~~Q-03~~ | ~~Par où entre la clé privée lors d'une restauration ?~~ | rédacteur | 2026-09-18 | `E-074`, `E-084`, `E-101` | **2026-09-18 → ADR-0007** |
| ~~Q-04~~ | ~~Les destinataires de chiffrement sont-ils déclarés par base ou globalement ?~~ | rédacteur | 2026-09-18 | `E-073`, `E-037`, `E-132` | **2026-09-22 → ADR-0016** : globaux, une liste par base les **remplace** |
| Q-05 | `min_interval` est-il un intervalle minimal ou maximal ? | rédacteur | 2026-09-18 | `E-091` | |
| Q-06 | Une base a-t-elle deux jeux d'identifiants, sauvegarde et restauration ? | rédacteur | 2026-09-18 | `E-084`, `E-086`, `E-116` | |
| ~~Q-07~~ | ~~Que sauvegarde-t-on exactement : une base, ou aussi les objets globaux du cluster ?~~ | rédacteur | 2026-09-18 | `E-058`, `E-084` | **2026-09-22 → ADR-0016** : une base, sans droits ni propriétaires ; les objets globaux remontent (`B-01`) |
| ~~Q-08~~ | ~~Huit valeurs par défaut absentes du CDC~~ | rédacteur | 2026-09-18 | `E-054`, `E-060`, `E-061`, `E-078`, `E-092`, `E-094` | **2026-09-22 → ADR-0016** pour celles du lot 2 ; les autres restent ouvertes |
| Q-09 | Sémantique exacte de la rétention (seaux, moment, destinations, suppression impossible) | rédacteur | 2026-09-18 | `E-077`, `E-071` | |
| Q-10 | Quelle authentification pour l'interface web hors `127.0.0.1` ? | rédacteur | 2026-09-18 | `E-099` | |
| Q-11 | Si HTMX (`F11.5`, souhaitable) est écarté, quel est le repli ? | rédacteur | 2026-09-18 | `E-102` | |
| Q-12 | La relecture à chaud est-elle obligatoire (§ 4.3) ou souhaitable (`F1.4`) ? | rédacteur | 2026-09-18 | `E-035` | |
| Q-13 | Si l'armement (`F8.6`, souhaitable) est écarté, la restauration est-elle ouverte par défaut ? | rédacteur | 2026-09-18 | `E-087` | |
| ~~Q-14~~ | ~~MySQL (Oracle) et PostgreSQL 12 sont-ils réellement supportés ?~~ | rédacteur | 2026-09-18 | `E-011`, `E-123` | **2026-09-18 → ADR-0004** |
| ~~Q-15~~ | Quelle signature protège les archives d'outils, et avec quelle clé ? | rédacteur | 2026-09-18 | `E-044` | |
| Q-16 | Quel protocole de liaison : fréquence, format, file locale, cycle de vie du jeton ? | rédacteur | 2026-09-18 | `E-106`, `E-107`, `E-111` | |
| ~~Q-17~~ | ~~En quelle langue parlent la CLI, l'interface et les messages ?~~ | rédacteur | 2026-09-18 | ADR langues, `E-100` | **2026-09-18 → ADR-0003** |
| Q-18 | Que fait le planificateur les nuits de changement d'heure ? | rédacteur | 2026-09-18 | `E-088` | |
| Q-19 | Que devient le catalogue quand la configuration d'une base change ? | rédacteur | 2026-09-18 | `E-028` | |
| Q-20 | `koffr tools install` existe-t-il sur **macOS** ? (Windows retiré par ADR-0011) | rédacteur | 2026-09-18 | `E-043` | partiellement, 2026-09-18 |
| Q-21 | Quelle purge pour `job_logs` et le fichier de journal ? | rédacteur | 2026-09-18 | `E-121`, `E-028` | |
| Q-22 | Reprend-on un dépôt d'archives existant (Portabase ou autre) ? | rédacteur | 2026-09-18 | — | |
| Q-23 | Quand la liaison au serveur doit-elle exister : « dès le premier jour » ou en `L5` ? | rédacteur | 2026-09-18 | `E-017`, roadmap | |

## Questions

### Q-01 — Quel mode de tampon s'applique quand la configuration ne dit rien ?

**Contexte** : le § 4.5 présente `stage` comme le « **Défaut** » dans sa table des modes, et `auto`
comme la « valeur recommandée en configuration ». `F3.4` écrit « mode `auto` par défaut ». Les deux
phrases ne peuvent pas être vraies ensemble.
**Question** : une base qui ne déclare pas `staging:` tourne-t-elle en `stage` ou en `auto` ?
— `stage` : comportement identique et prévisible partout, au prix d'un disque qui doit toujours
tenir la taille compressée × 1,5, sinon le job échoue.
— `auto` : l'agent s'adapte à la machine, mais le mode effectif peut changer d'une nuit à l'autre
selon l'espace libre, et deux exécutions ne se comparent plus.
**Hypothèse en attendant** : `auto`, parce que la configuration cible du § 5.1 l'écrit
(`staging: auto`) et que c'est ce que `doctor` est censé expliquer. Annoncée en `N-n` du plan du lot
qui livre le pipeline.

### Q-02 — La vérification relit-elle la destination ou le fichier tampon ?

**Contexte** : `F4.1` exige que l'empreinte soit « recalculée à la relecture **de la destination**
pour au moins une destination par sauvegarde ». Mais le § 4.5 justifie le mode `stage` en expliquant
que vérifier une archive distante « suppose de la retélécharger — de l'egress facturé, chaque nuit ».
Trois exigences dépendent de la réponse : `F5.3` (le job est réussi si au moins une destination a
« reçu **et vérifié** »), `F7.3` (seules les archives vérifiées comptent en rétention) et `P4`.
**Question** : quand une sauvegarde part vers `disque-local` **et** `s3-ovh`, que vérifie-t-on ?
— La destination locale seulement (pas d'egress, mais S3 n'est jamais contrôlé et une archive
corrompue à l'envoi ne se voit pas) ;
— la destination distante à chaque fois (egress nocturne facturé) ;
— le fichier tampon avant envoi, plus l'empreinte renvoyée par la destination quand elle en fournit
une (`ETag` S3), sans retéléchargement.
**Sous-question** : une archive vérifiée sur une destination et absente d'une autre est-elle
« vérifiée » au sens de la rétention (`F7.3`), et le catalogue affiche-t-il l'état par destination ?
**Hypothèse en attendant** : vérification sur la copie locale ou le tampon, vérification distante
seulement quand aucune copie locale n'existe. À annoncer dans le plan.

### Q-03 — Par où entre la clé privée lors d'une restauration ?

**Contexte** : `F6.3` dit que la clé privée « n'est jamais requise par l'agent, ni présente dans sa
configuration » et que « le déchiffrement est une opération manuelle et distincte ». Mais `F8`
décrit `keeper restore <db> --from <backup-id>` comme une commande complète, `F8.3` contrôle en
amont que l'archive est « déchiffrable », et `F11.4` propose l'action depuis l'interface web. Le
document ne dit nulle part comment l'archive est déchiffrée. C'est le seul point où nous ne voyons
pas de lecture cohérente du CDC.
**Question** : laquelle de ces trois intentions est la bonne ?
— **A. La clé est fournie à la commande** (`--identity FICHIER`, ou lue sur l'entrée standard) et
n'est jamais écrite sur disque : `keeper restore` reste une commande complète, et `F6.3` signifie
« jamais **stockée** par l'agent ». L'action de l'interface web (`F11.4`) doit alors demander la clé
à l'opérateur, ou être retirée.
— **B. Le déchiffrement est réellement hors Keeper** : l'opérateur fait `age -d` puis
`keeper restore --from-file archive.dump`. `F8.3` ne contrôle alors que la présence du fichier clair.
— **C. La clé est accessible à l'agent par un canal externe** (agent SSH, Vault, `systemd`
credentials) au moment de la restauration seulement.
**Ce que chacune implique** : A et C rendent la restauration possible depuis l'interface web ; B la
rend impossible et change `F11.4`. A est la plus simple à tester.
**Hypothèse en attendant** : A. Aucune restauration n'est codée avant la réponse.

### Q-04 — Les destinataires de chiffrement sont-ils déclarés par base ou globalement ?

**Contexte** : `F6.2` exige « plusieurs destinataires possibles **par base** — clé opérationnelle et
clé de séquestre ». La configuration cible du § 5.1 ne porte qu'un `encryption.recipients_file`
global, et aucune clé `recipients` sous `databases`. Le § 11 ajoute que les destinataires multiples
doivent être « obligatoires dès la configuration initiale ».
**Question** : la configuration doit-elle accepter une liste de destinataires par base, qui remplace
ou complète la liste globale ? Si oui, laquelle l'emporte quand les deux existent ?
**Hypothèse en attendant** : liste globale obligatoire, liste par base facultative qui **remplace**
la globale si elle est présente. Le champ par base est prévu dans le schéma dès le départ, même s'il
reste vide, pour ne pas casser la configuration plus tard.

### Q-05 — `min_interval` est-il un intervalle minimal ou maximal ?

**Contexte** : la configuration commente `min_interval: 24h` comme « plancher opposable au serveur
central ». `F9.4` : « une fréquence proposée **plus lâche** que le plancher est rejetée et alertée ».
Une fréquence plus lâche que 24 h correspond à un intervalle **plus long** que 24 h : le champ
plafonne donc l'intervalle entre deux sauvegardes, ce qu'un nom commençant par `min_` dit à
l'envers. Un opérateur qui lit `min_interval: 24h` comprendra « pas plus d'une sauvegarde par jour »,
c'est-à-dire l'inverse de la protection voulue.
**Question** : confirmez-vous que l'intention est « au moins une sauvegarde toutes les 24 h, et le
serveur ne peut pas proposer moins » ? Si oui, renomme-t-on le champ (`max_interval`,
`min_frequency`) ou garde-t-on `min_interval` pour ne pas dévier du CDC ?
**Hypothèse en attendant** : l'intention est bien « au moins une sauvegarde par `min_interval` ». Le
nom du CDC est conservé tel quel jusqu'à la réponse ; un renommage après coup casserait les
configurations existantes.

### Q-06 — Une base a-t-elle deux jeux d'identifiants, sauvegarde et restauration ?

**Contexte** : le § 6.2 recommande « un utilisateur de base dédié, en lecture seule pour la
sauvegarde, **distinct de l'utilisateur de restauration** ». Or une entrée `databases` ne porte qu'un
`user` et un `password_*`. `F8.3` contrôle par ailleurs « les droits suffisants » avant restauration,
ce qu'un utilisateur en lecture seule n'aura jamais. Et `--into DSN` (`F8.5`) ne dit pas d'où
viennent les identifiants de la base cible.
**Question** : la configuration porte-t-elle un second jeu d'identifiants de restauration par base ?
Sinon, la recommandation du § 6.2 est inapplicable et devrait être retirée du document.
**Hypothèse en attendant** : un bloc `restore:` facultatif par base (`user`, `password_*`), et un
`--into` qui prend une chaîne de connexion complète fournie à la commande. Rien n'est codé avant
la réponse à `Q-03`, qui touche la même commande.

### Q-07 — Que sauvegarde-t-on exactement : une base, ou aussi les objets globaux ?

**Contexte** : une entrée de configuration désigne une base unique (`database: boutique`). L'`argv`
du manifeste montre `--no-owner --no-privileges`, ce qui signifie que **ni les propriétaires ni les
droits ne sont restaurés**. Le CDC ne parle nulle part des objets globaux d'un cluster PostgreSQL
(rôles, tablespaces, paramètres), ni de la sauvegarde de plusieurs bases d'une même instance.
**Question** : en trois points.
— Une restauration doit-elle rendre les droits et les propriétaires d'origine, ou est-ce volontaire
de les perdre (cas classique d'une restauration vers une instance dont les rôles diffèrent) ?
— Faut-il une sauvegarde des objets globaux (`pg_dumpall --globals-only`) à côté de chaque base, ou
comme entrée de configuration à part ?
— Une entrée peut-elle désigner « toutes les bases de l'instance » ?
**Ce que ça change** : le scénario 5 du § 8 vérifie l'égalité du nombre de lignes par table, ce qui
passe même sans droits ni rôles. Le critère d'acceptation ne détectera donc pas le manque.
**Hypothèse en attendant** : une entrée = une base, `--no-owner --no-privileges` conservés tels
quels (c'est le CDC), objets globaux hors périmètre du MVP et notés au backlog.

### Q-08 — Huit valeurs par défaut que le CDC ne donne pas

**Contexte** : huit réglages sont décrits comme « configurable » ou « avec marge » sans valeur ni
clé de configuration. Les inventer, c'est trancher seul huit fois.
**Question** : quelle valeur par défaut pour chacun ?

| Réglage | Exigence | Ce que le CDC dit |
| ------- | -------- | ----------------- |
| Tolérance avant `backup_missed` | `E-092` | « l'intervalle attendu majoré d'une tolérance » |
| `N` sondes avant `database_unreachable`, et fréquence de la sonde | `E-092` | « N fois consécutives » |
| Seuil de bascule vers `-Fd` | `E-054` | « au-delà d'un seuil configurable » |
| Délai de grâce `SIGTERM` | `E-060` | « délai de grâce configurable » |
| Plancher d'archives valides de la rétention | `E-078` | « moins d'archives valides que le plancher configuré », sans clé dans la configuration cible |
| Délai de rappel d'une alerte | `E-094` | « après un délai de rappel configurable » |
| Niveau de compression zstd | `E-025` | le manifeste montre `zstd:3` ; est-ce le défaut, est-ce réglable ? |
| Marge d'espace disque avant un job | `E-061` | « avec marge » ; le § 4.5 donne « ≥ taille compressée estimée × 1,5 » pour `stage` |

**Hypothèse en attendant** : tolérance `backup_missed` = 25 % de l'intervalle ; 3 sondes toutes les
5 min ; seuil `-Fd` = 20 Go ; grâce `SIGTERM` = 5 min ; plancher rétention = 3 archives valides ;
rappel d'alerte = 24 h ; `zstd:3` réglable par base ; marge = ×1,5 partout. Chaque valeur est
inscrite en `N-n` dans le plan du lot concerné et signalée en recette.

### Q-09 — Sémantique exacte de la rétention

**Contexte** : `F7.1` énonce une politique `last` / `daily` / `weekly` / `monthly` sans dire comment
les seaux se calculent. Une règle de suppression sans définition exacte fait perdre des données.
**Question** : en cinq points.
— Dans quel fuseau les jours, semaines et mois se découpent-ils : celui de `agent.timezone`, ou UTC ?
— Quand deux sauvegardes réussissent le même jour, laquelle est « la » quotidienne : la première, la
dernière, la plus grosse ?
— La semaine commence-t-elle lundi ? Le seau mensuel retient-il la première ou la dernière du mois ?
— Quand la rétention s'exécute-t-elle : après chaque sauvegarde réussie, ou sur son propre planning ?
— Que fait-on quand la destination **refuse** la suppression (Object Lock, `F5.6`) : échec, alerte,
ou marquage « à expirer par le stockage » ? Et la politique se compte-t-elle globalement ou
**par destination** (une archive peut exister sur S3 et plus sur le disque) ?
**Hypothèse en attendant** : fuseau de l'agent, dernière sauvegarde réussie du jour, semaine ISO
(lundi), dernière du mois, rétention après chaque sauvegarde réussie, suppression impossible
journalisée et alertée sans faire échouer le job, décompte **par destination**.

### Q-10 — Quelle authentification pour l'interface web hors `127.0.0.1` ?

**Contexte** : `F11.2` exige « une authentification configurée » pour écouter ailleurs que sur la
boucle locale, faute de quoi l'agent refuse de démarrer. Mais le § 3 met l'authentification
multi-utilisateur et le RBAC hors périmètre, et le § 10 justifie l'absence d'authentification par le
fait que « l'interface locale est déjà protégée par l'accès à la machine ».
**Question** : quelle forme prend cette authentification dans le MVP ?
— Un mot de passe unique haché en configuration (simple, un seul opérateur, pas de traçabilité) ;
— un jeton statique en en-tête (pratique pour les scripts, inconfortable au navigateur) ;
— aucune, et l'écoute hors boucle locale est purement et simplement **refusée** dans le MVP (le plus
sûr, et cohérent avec « le RBAC est hors périmètre »).
**Hypothèse en attendant** : la troisième — refus de toute écoute hors `127.0.0.1` dans le MVP, avec
un message qui renvoie vers un tunnel SSH ou un proxy inverse.

### Q-11 — Si HTMX est écarté, quel est le repli ?

**Contexte** : `F11.5` (« rendu serveur, interactions par HTMX, pas de SPA, pas de chaîne de
compilation JavaScript ») est marqué **Souhaitable**, alors que `F11.1` (ressources embarquées,
aucun accès réseau sortant) et le § 12 (« HTMX est embarqué dans le binaire, jamais chargé depuis un
CDN ») supposent ce choix acquis. Écarter un souhaitable doit laisser quelque chose à sa place.
**Question** : `F11.5` est-il en réalité obligatoire — ce que la cohérence du reste suggère — ou
accepte-t-on une interface en HTML pur sans HTMX, à rechargement complet ?
**Hypothèse en attendant** : traité comme obligatoire ; c'est le seul choix compatible avec `F11.1`
et l'objectif du binaire unique.

### Q-12 — La relecture à chaud est-elle obligatoire ou souhaitable ?

**Contexte** : le § 4.3 décrit `keeper.yaml` comme « configuration, **lue à chaud** » — présenté
comme un fait de l'architecture. `F1.4` classe la même fonction en **Souhaitable**.
**Question** : un opérateur qui modifie `keeper.yaml` doit-il redémarrer le service dans le MVP ?
**Hypothèse en attendant** : souhaitable (`F1.4` fait autorité, c'est la table des exigences). Le
redémarrage reste nécessaire, et le § 4.3 sera corrigé.

### Q-13 — Si l'armement de la restauration est écarté, la restauration est-elle ouverte ?

**Contexte** : `F8.6` (`allow_restore: false` par défaut, armement par base expirant) est
**Souhaitable**. La configuration cible affiche pourtant `allow_restore: false` comme un réglage
normal, et le § 6 fait de l'impossibilité d'une restauration non voulue un élément du contrat de
sécurité.
**Question** : dans un MVP qui livrerait `F8` sans `F8.6`, une restauration locale est-elle possible
sans armement préalable ? Autrement dit : le **défaut à `false`** est-il obligatoire même si
**l'armement expirant** est souhaitable ?
**Hypothèse en attendant** : oui — `allow_restore: false` par défaut est traité comme obligatoire,
l'expiration automatique de l'armement comme souhaitable. C'est la lecture qui préserve le § 6.

### Q-14 — MySQL (Oracle) et PostgreSQL 12 sont-ils réellement supportés ?

**Contexte** : le § 3 annonce « PostgreSQL 12–18, MySQL 8.x, MariaDB 10.6+ ». `N7` n'exige des tests
d'intégration réels que contre « PostgreSQL 13 à 18 et MariaDB 10.6, 10.11 et 11.4 ». MySQL d'Oracle
n'est donc testé nulle part, alors que `F2.4` fait de la non-confusion des deux familles un point dur
du produit, et que `mysqldump` d'Oracle est la moitié de cette règle.
**Question** : en deux points.
— MySQL 8.x est-il dans le périmètre du MVP ? Si oui, `N7` doit l'inclure ; sinon, le § 3 doit le
retirer et le renvoyer en phase 2.
— PostgreSQL 12 (fin de support amont en novembre 2024) est-il maintenu dans le périmètre alors que
`N7` démarre à 13 ?
**Hypothèse en attendant** : le périmètre du § 3 fait foi (MySQL 8.x et PostgreSQL 12 dedans) et la
matrice `N7` est incomplète ; les tests d'intégration sont écrits pour couvrir ce que le § 3
annonce. C'est l'hypothèse la plus coûteuse : elle est à confirmer avant d'écrire la matrice de CI.

### ~~Q-15~~ — Quelle signature protège les archives d'outils, et avec quelle clé ? — **sans objet** depuis le 2026-09-19 (ADR-0014)

**Sans objet** : l'installation gérée sort du MVP, il n'y a plus d'archive d'outil à signer. La
question se rouvrira avec `E-043` si l'installation gérée revient.


**Contexte** : `F2.7` exige que toute archive téléchargée soit vérifiée « contre une empreinte
SHA-256 épinglée dans la version de l'agent, **et sa signature contrôlée** avant première
exécution ». L'empreinte épinglée est claire. La signature ne l'est pas : le CDC ne dit ni qui signe,
ni avec quelle clé, ni où cette clé est embarquée.
**Question** : la signature est-elle (a) celle de l'amont (dépôt de la distribution, clé PGP du
projet PostgreSQL), (b) la nôtre, produite par la chaîne de publication de `E-131`, ou (c) une
redondance de l'empreinte épinglée, auquel cas elle peut disparaître de l'exigence ?
**Lien** : dépend de `D-06` (qui construit et héberge les outils).
**Hypothèse en attendant** : (b), avec une clé publique embarquée dans le binaire. Rien n'est codé
avant `D-06`.

### Q-16 — Quel protocole de liaison ?

**Contexte** : le § 3 décide que le protocole du serveur central est dans le MVP « pour que la
frontière de confiance soit posée avant qu'on puisse la contourner ». Mais `F13` en décrit la
**politique** (qui pousse, ce qui est refusé) et jamais la **forme**.
**Question** : en quatre points.
— À quelle fréquence l'agent pousse-t-il, et pousse-t-il aussi immédiatement sur événement critique ?
— Quel format de message : un envoi complet de l'état, ou un différentiel ?
— La file locale de `F13.7` a-t-elle une taille maximale et une durée de rétention ? Que fait-on
quand elle déborde ?
— Comment le jeton est-il créé, distribué et renouvelé ? Un agent s'enrôle-t-il auprès du serveur,
ou l'opérateur colle-t-il un jeton dans un fichier ?
**Lien** : `D-08` (où vit le serveur) et `Q-23` (quand la liaison existe).
**Hypothèse en attendant** : aucune. Ce sont les seules exigences du MVP dont la conception ne peut
pas se deviner ; le lot correspondant ne démarre pas sans réponse.

### Q-17 — En quelle langue parlent la CLI, l'interface et les messages ?

**Contexte** : le CDC est en français ; la configuration, les événements (`backup_missed`), les modes
(`drop-schemas`) et les commandes sont en anglais. Le document ne dit jamais en quelle langue
s'adresser à l'opérateur. `CLAUDE.md` exige que cette langue soit fixée par un ADR.
**Question** : pour un outil d'exploitation destiné à des administrateurs système, dont les
identifiants techniques sont en anglais et dont les messages d'erreur seront collés dans des
recherches en ligne, la CLI et l'interface parlent-elles anglais ou français ? Et la documentation
d'installation ?
**Ce que ça change** : si le projet vise une diffusion publique (voir `D-02`, licence), l'anglais
est la seule option praticable. S'il reste interne, le français est plus confortable.
**Hypothèse en attendant** : interface et messages en **anglais**, pilotage du projet en français
(`METHODE.md`). Un ADR est proposé à l'étape 2.

### Q-18 — Que fait le planificateur les nuits de changement d'heure ?

**Contexte** : `F1.5` impose un fuseau explicite (`Europe/Paris`) précisément pour ne pas dépendre du
système. Mais deux nuits par an, une échéance à 2 h 30 n'existe pas (passage à l'heure d'été) ou
existe deux fois (passage à l'heure d'hiver). Le CDC ne dit rien, et `F9.2` interdit les rafales de
rattrapage.
**Question** : une sauvegarde planifiée à 2 h 30 est-elle, la nuit où cette heure n'existe pas,
(a) exécutée à 3 h 00, (b) sautée avec un `backup_missed`, ou (c) sautée silencieusement ? Et la nuit
où elle existe deux fois, tourne-t-elle une fois ou deux ?
**Hypothèse en attendant** : heure manquante → exécutée au premier instant valide ; heure doublée →
exécutée une seule fois. C'est le comportement de `robfig/cron` avec fuseau, à vérifier.

### Q-19 — Que devient le catalogue quand la configuration d'une base change ?

**Contexte** : le § 4.4 décrit la table `databases` comme « un instantané de la configuration
résolue, **pour détecter les changements** ». Le CDC ne dit jamais ce qu'on fait d'un changement
détecté.
**Question** : quand l'opérateur renomme `boutique-prod` en `shop-prod`, change son hôte ou retire
l'entrée : que deviennent les archives déjà cataloguées sous l'ancien identifiant, leur rétention et
leur chemin distant (`F5.5` construit le chemin sur le nom de la base) ? Une alerte est-elle émise ?
**Hypothèse en attendant** : l'identifiant de base est la clé stable ; un renommage est traité comme
une base nouvelle (catalogue ancien conservé, plus aucune rétention appliquée dessus) et signalé au
démarrage. Le retrait d'une entrée ne supprime jamais d'archives.

### Q-20 — `keeper tools install` existe-t-il ailleurs que sur Linux ?

**Contexte** : `F2.6` décrit des « bibliothèques partagées embarquées » et un « `RPATH` ajusté » :
c'est du vocabulaire ELF, donc Linux. `N1` publie pourtant aussi pour `darwin/arm64` et
`windows/amd64`, et le risque numéro un du § 11 ne se valide que sur trois distributions Linux.
**Question** : sur macOS, l'agent se contente-t-il des outils présents sur l'hôte et de la stratégie
`exec`, `koffr tools install` renvoyant une erreur claire ?
**Réponse partielle du 2026-09-18** : ADR-0011 retire Windows du périmètre et tranche le cas Linux
(installation gérée) ; la question ne porte plus que sur macOS, plateforme de développement.
**Hypothèse en attendant** : oui — installation gérée sur Linux uniquement, erreur explicite sur
macOS.

### Q-21 — Quelle purge pour `job_logs` et le fichier de journal ?

**Contexte** : `job_logs` accumule « des lignes de journal structurées rattachées à un job » (§ 4.4)
et `N5` prévoit une rotation pour le fichier `keeper.log`, mais rien pour la base. Un agent qui
sauvegarde dix bases chaque nuit pendant deux ans accumule sans limite.
**Question** : combien de temps garde-t-on les journaux de jobs en base, et l'historique des jobs
lui-même ? L'historique des suppressions de `F7.5` est explicitement conservé « même après
disparition de l'archive » — s'applique-t-il la même règle ?
**Hypothèse en attendant** : `job_logs` purgé au-delà de 90 jours, `jobs` et `alerts` conservés
un an, historique de rétention (`F7.5`) conservé sans limite. Valeurs configurables.

### Q-22 — Reprend-on un dépôt d'archives existant ?

**Contexte** : le § 1.1 part de l'insatisfaction envers des outils existants, et le colophon indique
que le document est né de l'analyse de Portabase. Rien ne dit si des archives produites par l'outil
remplacé doivent être adoptées par Keeper.
**Question** : Keeper doit-il savoir lire, cataloguer ou restaurer des archives qu'il n'a pas
produites ? Ou la migration consiste-t-elle à faire tourner les deux outils en parallèle le temps
que la rétention de l'ancien expire ?
**Hypothèse en attendant** : aucune reprise ; les deux outils cohabitent le temps de la transition.
Si la réponse est « oui », c'est une exigence nouvelle, absente du CDC, et un lot à part entière.

### Q-23 — Quand la liaison au serveur doit-elle exister ?

**Contexte** : le § 3 écrit que « l'agent doit pouvoir s'y connecter **dès le premier jour** ». Le
§ 9 place la liaison au dernier lot (`L5`). Le § 8 ajoute que le scénario 10 — le rejet d'un ordre de
restauration — « doit exister dès le premier lot qui active la liaison au serveur ».
**Question** : « dès le premier jour » veut-il dire (a) le protocole est **défini** (schéma des
messages, règles de validation) dès le début et **implémenté** en `L5`, ou (b) la liaison est
fonctionnelle bien plus tôt ?
**Ce que ça change** : dans le cas (a), la roadmap peut suivre le découpage du § 9 ; dans le cas (b),
la liaison remonte avant l'interface.
**Hypothèse en attendant** : (a). C'est ce que dit la décision de cadrage du § 3 (« en le définissant
d'abord, la frontière de confiance est posée »), qui parle de définition, pas de livraison.

## Réponses reçues

### Q-03 — La clé privée est fournie à la commande de restauration

Reçue le 2026-09-18 du propriétaire. Option A : `koffr restore --identity <fichier|->`, la clé est
lue en mémoire, jamais écrite ni mémorisée ; `F6.3` se lit « jamais **stockée** par l'agent ».
Ce qu'on en a fait : **ADR-0007**, qui en tire une conséquence à signaler — l'interface web
n'exécute plus la restauration dans le MVP, elle affiche la commande à lancer (`E-101` réduite).

### Q-14 — Le périmètre du § 3 fait foi, la matrice de tests est complétée

Reçue le 2026-09-18 du propriétaire. MySQL 8.x et PostgreSQL 12 restent dans le périmètre ; `N7`
(`E-123`) est élargie pour les couvrir. Ce qu'on en a fait : **ADR-0004**.

### Q-17 — Le produit parle anglais

Reçue le 2026-09-18 du propriétaire. CLI, interface, messages d'erreur, journaux et documentation
en anglais ; pilotage du projet en français ; aucun mécanisme de traduction dans le MVP.
Ce qu'on en a fait : **ADR-0003**.
