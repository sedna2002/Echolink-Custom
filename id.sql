-- phpMyAdmin SQL Dump
-- version 5.2.1
-- https://www.phpmyadmin.net/
--
-- Hôte : 127.0.0.1:3306
-- Généré le : mar. 11 nov. 2025 à 22:48
-- Version du serveur : 9.1.0
-- Version de PHP : 8.3.14

SET SQL_MODE = "NO_AUTO_VALUE_ON_ZERO";
START TRANSACTION;
SET time_zone = "+00:00";


/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!40101 SET NAMES utf8mb4 */;

--
-- Base de données : `echolink`
--

-- --------------------------------------------------------

--
-- Structure de la table `id`
--

DROP TABLE IF EXISTS `id`;
CREATE TABLE IF NOT EXISTS `id` (
  `id` int NOT NULL AUTO_INCREMENT,
  `indicatif` varchar(20) DEFAULT NULL,
  `idEcholink` varchar(20) DEFAULT NULL,
  `n` int NOT NULL,
  `date_connexion` datetime DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
) ENGINE=MyISAM AUTO_INCREMENT=3 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

--
-- Déchargement des données de la table `id`
--

INSERT INTO `id` (`id`, `indicatif`, `idEcholink`, `n`, `date_connexion`) VALUES
(1, 'F4AMY', '228230', 0, '2025-11-11 23:33:37'),
(2, 'F4AMY', '228230', 0, '2025-11-11 23:33:48');
COMMIT;

/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
