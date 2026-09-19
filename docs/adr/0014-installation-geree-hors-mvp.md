# ADR-0014 — L'installation gérée d'outils sort du MVP : l'hôte et le conteneur suffisent

- **Date** : 2026-09-19
- **Statut** : **accepté** — par le propriétaire, le 2026-09-19
- **Exigences** : `E-013`, `E-042`, `E-043`, `E-044`, `E-045`, `E-103a`, `E-131`
- **Références** : tranche `D-06` et rend `Q-15` sans objet ; CDC § 5.2 `F2.6` à `F2.8`, § 8
  scénarios 2 et 3, § 11 ; lié à ADR-0011 (Windows) et au spike `E-130`

## Contexte

`F2.6` et `F2.7` supposent une infrastructure permanente : construire **sept versions de
PostgreSQL et trois de MariaDB, sur deux architectures**, avec bibliothèques embarquées et `RPATH`
ajusté, les signer, les héberger, épingler leurs empreintes dans chaque version de l'agent, et
**recommencer à chaque version amont**. Le § 11 le qualifie lui-même de « coût récurrent à
assumer ». `D-06` posait la question depuis le démarrage du projet et bloquait une vague entière du
lot 1 ; `Q-15` — quelle signature, quelle clé — n'avait pas de réponse non plus.

Trois faits mesurés pendant les lots 0 et 1 éclairent la décision.

1. **La technique n'est pas le problème.** Le spike `E-130` a prouvé qu'un outil peut être livré
   avec ses bibliothèques et exécuté sur Debian, Rocky **et Alpine**, jusqu'à la sauvegarde réelle
   d'une base par TCP. Ce qui manquait n'était pas un doute technique, c'était un engagement.
2. **Deux des trois sources de `E-013` sont déjà livrées et testées** : les outils de l'hôte
   (vague 2) et la stratégie `exec` dans le conteneur de la base (vague 5), cette dernière vérifiée
   contre un vrai conteneur MariaDB 11.4.
3. **Une machine qui héberge un serveur de base héberge presque toujours son client.**
   `postgresql-16` dépend de `postgresql-client-16` ; l'image MariaDB livre `mariadb-dump`. Le cas
   où le client manque vraiment est celui d'un agent de sauvegarde distant face à un parc
   hétérogène — et là, les paquets clients coexistent (`postgresql-client-16` et `-17` s'installent
   côte à côte) ou la stratégie `exec` répond.

## Décision

**L'installation gérée d'outils sort du MVP.** Les sources d'outils sont l'**hôte** et le
**conteneur de la base**.

- `E-043`, `E-044`, `E-045` et `E-131` sont **reportées après la mise en service**, avec leur ligne
  au registre et au backlog. Le travail déjà fait — le spike, la provenance `managed` du modèle,
  l'énumération de `/var/lib/koffr/tools/` — est **conservé** : le répertoire est simplement vide.
- **`koffr tools install` et `koffr tools remove` n'existent pas.** `koffr tools list` reste, et
  c'est lui qui rend `E-038` observable. `E-103a` est amendée d'autant.
- **`E-042` devient partiellement couverte, et c'est écrit comme tel.** Le message d'échec nomme
  la version attendue et les versions trouvées, avec leur provenance ; il **ne donne pas de
  commande**, parce que koffr n'en a aucune à offrir. Les clients de dump et de restauration
  deviennent un **prérequis**, porté par le `README`. Une table de correspondance par distribution
  aurait tenu l'exigence à la lettre, au prix exact de la charge récurrente que cette décision
  retire — le calcul ne vaut pas la peine au stade du MVP.
- **`D-06` est tranchée** et **`Q-15` devient sans objet** : il n'y a plus d'archive d'outil à
  construire, à signer ni à héberger.
- Le critère de sortie du lot 1 est amendé : les **scénarios 2 et 3 du § 8** ne sont pas joués. Le
  scénario 2 l'est à moitié — l'échec nommant la version manquante et la correction —, le 3 ne
  l'est pas.

## Conséquences

- **Le produit demande un geste à l'exploitant** là où le CDC promettait qu'il se débrouille seul,
  et il ne lui dit pas lequel. C'est le vrai coût de cette décision, visible à l'endroit exact où
  il se paie : le message d'échec. `E-042` reste **partiellement couverte** jusqu'à ce que
  l'installation gérée revienne ou qu'une table par distribution soit écrite.
- **`E-124`, l'image conteneur du lot 7, devient une question ouverte.** Sans installation gérée,
  l'image doit soit embarquer les clients — et grossir —, soit se reposer sur la stratégie `exec`
  et le montage du socket Docker. À trancher au lot 7, pas ici.
- Le spike `E-130` et son rapport **restent valables** : le jour où l'installation gérée revient,
  la technique est prouvée et documentée, y compris ses deux pièges (`DT_RUNPATH` ne s'hérite pas,
  `PT_INTERP` est absolu).
- Le binaire n'embarque pas de client de téléchargement d'outils, ce qui laisse de la marge sous le
  seuil de 30 Mo — déjà entamé à 10 Mio par le client Docker.
- **Ce qui rouvrirait la décision** : un exploitant réellement bloqué parce que sa distribution ne
  livre pas la version de client dont il a besoin ; ou l'image conteneur du lot 7 qui se révélerait
  impraticable sans elle.

## Alternatives écartées

- **Assumer l'infrastructure** (option 1 de `D-06`) : dix binaires par architecture, signés,
  hébergés, refaits à chaque version amont. L'engagement le plus lourd du CDC pour un projet porté
  par une personne, et il conditionnait une fonctionnalité dont deux substituts existent déjà.
- **Réempaqueter des paquets amont déjà signés** (option 2 de `D-06`) : moins cher que le 1, mais
  il reste à héberger, à épingler des empreintes dans chaque version de l'agent, et `Q-15` reste
  entière. Le gain sur l'option 1 ne paie pas la complexité restante au stade du MVP.
- **Garder `koffr tools install` en commande qui explique** : refusé par le propriétaire. Une
  commande qui n'existe pas rend `unknown command`, ce qui est un message clair ; et `koffr --help`
  ne promet alors que ce qu'il tient.
- **Détecter la distribution pour donner la commande exacte**, ou nommer les paquets des deux
  familles de distributions dans le message : le plus actionnable, et ce que `E-042` demande à la
  lettre. Écarté par le propriétaire — c'est une correspondance à tenir à jour, soit exactement la
  charge récurrente que cette décision retire. Le prérequis part dans le `README`.
