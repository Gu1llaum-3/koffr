# Recette lot 3 — Manifeste, catalogue et vérification

Le lot 2 a répondu à « l'archive est-elle utilisable ? ». Celui-ci répond à la question d'après, et
c'est celle du principe `P4` : **comment sait-on qu'elle l'est, et qu'est-ce qui le dit quand on a
tout perdu sauf le dépôt ?**

La question centrale de la session : **un dépôt d'archives s'inventorie-t-il sans koffr et sans la
clé privée ?** Si la réponse est non, le lot ne passe pas.

Durée attendue : une heure, préparation des bases non comprise.

## Avant de commencer

- La machine de recette des lots 1 et 2, avec son parc : PostgreSQL et MariaDB réelles, contenant
  **du volume** — quelques centaines de milliers de lignes. Une base de trois lignes ne montre rien.
- `age`, `zstd`, `jq` et les clients PostgreSQL installés (`apt install age zstd jq`).
- La paire de clés du lot 2, dont la privée est **hors de la machine**.
- `/srv/backups` avec les archives déjà produites au lot 2 : elles n'ont **pas** de manifeste, et
  c'est un cas qu'on veut voir.
- Prépare : le propriétaire.

## Ce qui est voulu et pourrait passer pour un bug

| Constat | Pourquoi c'est voulu |
| --- | --- |
| La structure est vérifiée **pendant** le dump, pas en relisant l'archive | **ADR-0017** : koffr ne détient qu'une clé publique (`E-113`). C'est le seul instant où il voit le dump en clair |
| `koffr verify` recalcule l'empreinte mais **ne rejoue pas** la structure | Même raison. Il le **dit** au lieu de laisser croire qu'il a tout revérifié |
| `pg_restore --list` sur un dump tronqué renvoie **0** | Mesuré au lot 3. Il valide la table des matières, pas le contenu : c'est l'**empreinte** qui voit une troncature |
| Un seul état de vérification par archive, pas un par destination | `N-5` : `Q-02` reste ouverte et se tranche au lot 4, qui apporte S3 et SFTP. Une colonne sera ajoutée par migration |
| Aucune revérification périodique des vieilles archives | `E-065`, lot 6 — et elle ne portera que sur l'empreinte, pour la raison d'ADR-0017 |
| Une seule destination possible | S3 et SFTP au lot 4 |
| `koffr restore` n'existe pas | Lot 4 |
| Les archives du lot 2 n'ont pas de manifeste | Elles ont été écrites avant. Rien ne les rattrape : `koffr list` doit les montrer **non vérifiées**, pas les cacher |

## Parcours

### 1. Le catalogue existe enfin

1. `koffr backup boutique` puis `koffr list` → **on doit voir** l'archive, sa base, sa date, sa
   taille et son **état de vérification**.
2. Ouvrir la base locale — `sqlite3 /var/lib/koffr/koffr.db "select id, database_id, verified from
   backups"` → **on doit voir** la ligne, et une ligne dans `databases` pour la base sauvegardée.
3. Modifier la configuration de la base — changer son port —, relancer une sauvegarde, et regarder
   `databases.fingerprint` → **on doit voir** l'empreinte **changer** (`E-028`, `CAT-01`).

**Décision attendue** : `koffr list` dit-il ce qu'il faut ? Que voudriez-vous y lire que vous n'y
lisez pas — la destination, la durée, le nom de l'outil ?

### 2. Vérifié ou non, ça se voit

1. `koffr list` sur un dépôt contenant **les archives du lot 2** (sans manifeste) et une nouvelle
   → **on doit voir** une différence **immédiate** à l'œil entre les deux (`E-064`).
2. Demander à quelqu'un qui n'a pas suivi le projet de dire, en regardant l'écran, lesquelles sont
   vérifiées → **il doit répondre juste sans explication**.

**Décision attendue** : la distinction est-elle assez forte ? Faut-il une couleur, un mot, les deux ?

### 3. L'archive est vraiment relue

1. `koffr verify <backup-id>` sur une archive saine → **on doit voir** l'empreinte recalculée,
   le catalogue mis à jour, et une phrase disant que la **structure** n'est pas rejouée et pourquoi.
2. Corrompre **un octet** au milieu de l'archive — `printf '\x00' | dd of=<archive> bs=1 seek=5000
   conv=notrunc` — puis `koffr verify <backup-id>` → **on doit voir** un **échec**, et le catalogue
   passer à `failed`.
3. `koffr list` après ça → **on doit voir** l'archive marquée en échec, pas simplement « non
   vérifiée ».

**Décision attendue** : aucune. C'est le critère de sortie n° 5.

### 4. Un dump qui n'en est pas un est refusé

1. Arrêter le serveur PostgreSQL **pendant** un `koffr backup` → **on doit voir** le job échouer, et
   **aucune archive** marquée vérifiée.
2. Regarder le journal → **on doit voir** l'étape qui a cassé, nommée.

**Décision attendue** : aucune.

### 5. Le dépôt s'inventorie sans koffr — le cœur de la session

C'est le parcours le plus important, et il se joue **sans lancer koffr une seule fois**.

1. `ls /srv/backups/boutique/2026/09/` → **on doit voir** l'archive **et** son `.json` à côté.
2. `jq -r '[.database_id, .started_at, .size_stored, .verified.checksum] | @tsv'
   /srv/backups/*/*/*/*.json` → **on doit voir** l'inventaire complet du dépôt : quelle base, quand,
   quelle taille, vérifiée ou non.
3. `jq . <un manifeste>` → **on doit voir** les champs de `E-058` : identifiant, base, moteur, début,
   durée, version du serveur, outil avec sa version, sa provenance, son chemin et son `argv`, format,
   chaîne de traitement, mode de tampon, tailles, empreintes, destinataires, état de vérification.
4. Chercher un identifiant de connexion dans **tous** les manifestes — `grep -rF '<mot de passe>'
   /srv/backups/` et la même chose pour l'utilisateur et l'hôte → **on ne doit rien trouver**
   (`E-059`, `E-114`).
5. Faire l'exercice complet : **depuis une machine qui n'a pas koffr**, avec le dépôt et la clé
   privée, retrouver le contenu d'une base — `age --decrypt | zstd -d | pg_restore`.

**Décision attendue** : le manifeste contient-il ce qu'il faut pour décider quoi restaurer, six mois
plus tard, sans koffr ? Manque-t-il un champ ?

### 6. Ce qu'un stockage compromis livre

1. Se mettre à la place de quelqu'un qui a **volé le dépôt** et rien d'autre : lire tous les
   manifestes → **on doit voir** des **métadonnées** — noms de bases, tailles, horaires — et rien
   d'autre (`E-114`).
2. Tenter d'ouvrir une archive sans la clé → **on doit voir** un refus.

**Décision attendue** : ces métadonnées sont-elles acceptables en clair ? Le § 6 les assume ; vues
sur un vrai dépôt, gardez-vous cette position ?

## Décisions attendues de la session

1. La lisibilité de `koffr list` et la force de la distinction vérifié / non vérifié (parcours 1, 2).
2. Les champs du manifeste : en manque-t-il un pour décider une restauration sans koffr (parcours 5) ?
3. Les métadonnées en clair d'un dépôt volé : position confirmée ou non (parcours 6) ?
4. **`Q-02` ne peut pas être tranchée ici** : elle demande S3, donc la recette du lot 4. `N-5` tient
   en attendant, et son coût — une colonne ajoutée par migration — est connu.

## Comment rapporter

Une anomalie = écran ou commande, geste, attendu, constaté, gravité (bloquant / gênant /
cosmétique). Dans `docs/recette/anomalies.md`, numérotée à la suite de `A-18`.
