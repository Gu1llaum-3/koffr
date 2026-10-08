# ADR-0018 — Le catalogue indexe, le dépôt fait foi

- **Date** : 2026-10-08
- **Statut** : accepté
- **Exigences** : `E-028`, `E-057`, `E-059`, `E-064`, `E-114`
- **Références** : ADR-0006 (qu'il précise), ADR-0017, anomalies `A-19` et `A-24`, `N-1` du plan
  `lot-3-corrections`

## Contexte

ADR-0006 pose que « le catalogue est un index, jamais la source de vérité ». La recette du lot 3,
jouée le 2026-10-08 sur un parc réel, a montré que la phrase n'était pas opposable : deux
situations concrètes la contredisaient dans les deux sens.

- **`A-19`** — une archive écrite par le binaire du **lot 2**, posée sur la destination et absente
  du catalogue, n'apparaissait **pas du tout** dans `koffr list`. Un exploitant qui met à jour
  perdait de vue tout son historique. Le catalogue se comportait donc comme la source de vérité :
  ce qu'il ignorait n'existait pas.
- **`A-24`** — `koffr verify` faisait passer le catalogue à `failed` sur une archive corrompue, et
  le manifeste déposé à côté continuait d'annoncer `"verified": {"checksum": true,
  "structure": true}`. Un inventaire fait **sans koffr**, qui est le critère de sortie du lot,
  rendait donc « vérifiée » une archive que koffr savait cassée.

Mesuré en séance : le manifeste porte `sha256_stored`, et `jq` + `sha256sum` suffisent à démasquer
l'archive corrompue sans koffr et sans clé privée. Le contrôle a été fait, et il a vu le défaut.

## Décision

**Le catalogue n'enregistre que ce que ce koffr a fait ; le dépôt dit ce qui existe ; le manifeste
est un instantané qui ne change plus.**

- `koffr list` lit le catalogue **et** les destinations déclarées, et montre les deux. Une archive
  que la destination tient et que le catalogue ignore est affichée, marquée `not in the catalogue`.
- **La fusion ne va que dans un sens.** Rien de ce qui est trouvé sur une destination n'est écrit
  au catalogue : il resterait la trace de ce qu'un autre agent, ou une autre version, a fait.
- Le manifeste d'une archive **n'est jamais réécrit** après le job qui l'a produit. Un `koffr
  verify` en échec met à jour le catalogue, pas le dépôt.
- Un manifeste trouvé renseigne les **faits** d'une archive inconnue — base, tailles, empreintes,
  horodatage — mais **jamais son état de vérification** : koffr n'a aucune trace de l'avoir
  contrôlée, et `E-064` interdit un blanc qui se lirait comme un succès.
- Une archive dont le catalogue parle et que la destination ne tient plus est affichée, et le dit.

## Conséquences

- **Un dépôt reste exploitable sans le catalogue**, qui est le critère de sortie du lot 3, et le
  catalogue reste reconstructible depuis les manifestes seuls (`CAT-02`).
- **Un lister doit lire la destination**, ce qui coûte un appel par destination déclarée à chaque
  `koffr list`. Borné par le préfixe : `<base>/` quand une base est demandée.
- **L'état de vérification d'une archive vit dans un seul endroit**, le catalogue. Qui n'a que le
  dépôt vérifie l'empreinte à la main — `jq` + `sha256sum` contre `sha256_stored` —, et le `README`
  le documente.
- La **rétention** du lot 6 raisonne sur le catalogue : elle ne supprimera jamais ce qu'elle n'a
  pas écrit, et une archive hors catalogue reste donc sur le disque indéfiniment. C'est voulu à ce
  stade ; une purge d'archives inconnues serait une décision à part.
- **Ce qui rouvrirait la décision** : un dépôt partagé par plusieurs agents, où chacun ignorerait
  les archives des autres au point de ne pas pouvoir appliquer une rétention commune ; ou une
  destination dont le listage coûterait assez cher pour qu'une liste quotidienne le fasse sentir —
  S3 au lot 4 le dira.

## Alternatives écartées

- **Une commande `koffr import`** qui balaie le dépôt et inscrit ce qu'elle trouve : un geste de
  plus à connaître, et un catalogue qui prétend avoir fait ce qu'il n'a pas fait.
- **Réécrire le manifeste à chaque vérification** : il devient mutable, chaque `verify` écrit sur
  chaque destination, et deux copies de la même archive peuvent diverger.
- **Un second fichier `<archive>.verified.json`** déposé par `verify` : le manifeste reste figé,
  mais un fichier de plus par archive, et un inventaire qui doit en lire deux.
- **Ne rien changer et documenter** que le catalogue ne décrit que ce que ce koffr a écrit : laisse
  une archive invisible, ce qui est pire qu'une archive non vérifiée (`E-064`).
