package hub

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kiryu-dev/tic-tac-toe/internal/domain"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

const (
	clientQueueBufSize = 2
	syncPeriod         = 5 * time.Second
)

type enqueuedClient struct {
	client     domain.Client
	resultChan chan domain.Player
}

type useCase struct {
	game        domain.GameUseCase
	clientQueue chan enqueuedClient
	gamesStates map[string]*domain.GameState
	statesChan  chan map[string]*domain.GameState
	ticker      *time.Ticker
	mu          *sync.RWMutex
	logger      *zap.Logger
}

func New(game domain.GameUseCase, logger *zap.Logger) useCase {
	u := useCase{
		game:        game,
		clientQueue: make(chan enqueuedClient, clientQueueBufSize),
		gamesStates: make(map[string]*domain.GameState),
		statesChan:  make(chan map[string]*domain.GameState),
		ticker:      time.NewTicker(syncPeriod),
		mu:          &sync.RWMutex{},
		logger:      logger,
	}
	go u.createGames()
	go u.syncStates()
	return u
}

func (u useCase) Handle(ctx context.Context, client domain.Client) error {
	player := u.enqueueForGame(client)
	u.mu.RLock()
	gameState := u.gamesStates[player.GameUuid()]
	u.mu.RUnlock()
	if err := u.game.Play(ctx, player, gameState); err != nil {
		return errors.WithMessage(err, "play game")
	}
	return nil
}

func (u useCase) enqueueForGame(client domain.Client) domain.Player {
	ch := make(chan domain.Player)
	defer close(ch)
	u.clientQueue <- enqueuedClient{
		client:     client,
		resultChan: ch,
	}
	return <-ch
}

func (u useCase) createGames() {
	for {
		for len(u.clientQueue) > 1 {
			lhs := <-u.clientQueue
			rhs := <-u.clientQueue
			gameUuid := u.createGame(lhs.client.Uuid(), rhs.client.Uuid())
			moveChan := make(chan domain.Move)
			lhs.resultChan <- domain.NewPlayer(gameUuid, lhs.client, domain.X, moveChan)
			rhs.resultChan <- domain.NewPlayer(gameUuid, rhs.client, domain.O, moveChan)
		}
	}
}

func (u useCase) createGame(playerX string, playerO string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	gameUuid := uuid.NewString()
	var board domain.Board
	for i := range board {
		board[i] = domain.None
	}
	u.gamesStates[gameUuid] = &domain.GameState{
		Board:       board,
		PlayerX:     playerX,
		PlayerO:     playerO,
		CurrentMove: domain.X,
		Status:      domain.ReadyToStart,
	}
	return gameUuid
}

func (u useCase) syncStates() {
	defer u.ticker.Stop()
	for range u.ticker.C {
		u.removeFinishedGames()
		u.statesChan <- u.gamesStates
	}
}

func (u useCase) GamesStates() <-chan map[string]*domain.GameState {
	return u.statesChan
}

func (u useCase) removeFinishedGames() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for gameUuid, state := range u.gamesStates {
		if state.Status == domain.Finished {
			delete(u.gamesStates, gameUuid)
		}
	}
}

func (u useCase) ApplyStates(_ context.Context, states map[string]*domain.GameState) {
	u.mu.Lock()
	u.gamesStates = states
	u.logger.Info("applied states", zap.Any("states", u.gamesStates))
	u.mu.Unlock()
}
