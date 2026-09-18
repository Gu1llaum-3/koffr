# ADR-0007 — L'agent ne détient qu'une clé publique ; la clé privée est fournie à la restauration

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-072`, `E-073`, `E-074`, `E-075`, `E-076`, `E-084`, `E-101`, `E-113`, `E-132`
- **Références** : répond à `Q-03` ; `Q-04` et `Q-06` restent ouvertes ; CDC § 5.6, § 5.8, § 6

## Contexte

Le CDC laisse ici une contradiction qu'il faut lever avant d'écrire la restauration. `E-074`
(`F6.3`) : la clé privée « n'est jamais requise par l'agent, ni présente dans sa configuration », et
« le déchiffrement est une opération manuelle et distincte ». Mais `E-084` (`F8.3`) contrôle en
amont que l'archive est « déchiffrable », `E-101` (`F11.4`) propose la restauration depuis
l'interface web, et le scénario 5 du § 8 exige une restauration de 5 Go depuis S3 avec vérification
du nombre de lignes. Le document ne dit nulle part par où entre la clé.

Le § 6 donne l'intention réelle : un agent compromis « n'obtient pas le déchiffrement des archives
passées : il ne détient qu'une clé publique ». Ce qui est interdit, c'est que la clé **réside** sur
la machine ou dans sa configuration — pas qu'elle traverse un processus le temps d'une restauration
décidée par un humain présent.

## Décision

L'agent ne connaît que des clés publiques. La clé privée est **fournie à la commande de
restauration**, en mémoire, jamais écrite ni mémorisée.

- `koffr restore <db> --from <backup-id> --identity <fichier|->` : la clé est lue depuis un fichier
  désigné à l'appel, ou sur l'entrée standard. Elle n'est jamais copiée sur disque, jamais
  journalisée, jamais envoyée au serveur central, et la mémoire est effacée en fin d'opération.
- Aucune clé privée n'apparaît dans `koffr.yaml`, ni dans aucun champ `*_file` ou `*_env` de la
  configuration : la configuration ne peut pas désigner une clé privée, l'analyse stricte (`E-032`)
  refuse la clé.
- `E-084` (contrôles préalables) vérifie que la clé fournie **est** un destinataire de l'archive, en
  lisant l'en-tête `age`, avant de toucher à la base cible.
- **L'interface web ne restaure pas dans le MVP.** `E-101` est réduite : l'écran affiche la commande
  exacte à exécuter, avec son identifiant d'archive et sa base cible, et ne l'exécute pas. Demander
  une clé privée dans un navigateur servi en clair sur une machine de production est un recul de
  sécurité que le § 6 ne permet pas.
- `koffr keygen` (`E-076`) affiche la paire une seule fois, n'écrit jamais la clé privée, et
  rappelle à l'écran qu'un second destinataire de séquestre est obligatoire (`E-132`).
- Au démarrage, tant qu'un seul destinataire est déclaré, un avertissement est émis (`E-132`).

## Conséquences

- La restauration reste **une seule commande**, testable de bout en bout, donc le scénario 5 du § 8
  se joue automatiquement en intégration continue. C'est le principal gain de cette décision.
- `E-101` change de portée : c'est une réduction d'exigence, et elle doit être signalée comme telle
  au propriétaire et en recette, pas glissée dans le code.
- Un opérateur qui perd la clé privée perd l'historique. C'est le risque n° 1 du § 11 et il est
  assumé : destinataires multiples obligatoires, séquestre documenté, avertissement au démarrage.
- La clé transite par la mémoire d'un processus qui a aussi accès aux bases. Un attaquant déjà
  présent **pendant** une restauration peut la capturer : c'est irréductible, et strictement moins
  grave que la clé au repos sur la machine.
- **Ce qui rouvrirait la décision** : un besoin réel de restauration déclenchée depuis l'interface,
  qui supposerait alors un canal de clé (agent SSH, Vault) et donc une dépendance de service que
  `P5` refuse.

## Alternatives écartées

- **Déchiffrement entièrement hors koffr** (`age -d` puis `koffr restore --from-file`) : lecture la
  plus littérale de `F6.3`, mais transforme le scénario 5 en manipulation manuelle en deux temps,
  avec un fichier clair de 40 Go posé sur le disque — ce que `E-025` s'interdit partout ailleurs.
- **Clé lue depuis un agent SSH ou Vault** : permettrait la restauration depuis l'interface web, au
  prix d'une dépendance de service contraire à `P5` et d'un test d'intégration difficile.
- **Clé privée en configuration avec permissions restreintes** : contredit frontalement `E-074` et
  annule la garantie du § 6 sur l'agent compromis.
