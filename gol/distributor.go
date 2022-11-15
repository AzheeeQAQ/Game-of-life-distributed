package gol

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"strconv"
	"sync"
	"time"
	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPresses <-chan rune
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	// TODO: Create a 2D slice to store the world.

	// create a lock
	var mu sync.Mutex
	//create a turn to store the turn
	var turn int

	//create a filename
	filename := strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(p.ImageHeight)
	c.ioFilename <- filename
	c.ioCommand <- ioInput

	//
	//stdin := make(chan bool)
	//go

	TestTry := make(chan bool)
	go handleServer(c, TestTry)

	ln, _ := rpc.Dial("rpc", "127.0.0.1:8030")
	defer ln.Close()
	//create a 2D world
	world := make([][]byte, p.ImageWidth)
	for i := range world {
		world[i] = make([]byte, p.ImageHeight)
	}

	//initialise the world
	for j := range world {
		for i := range world[0] {
			cell := <-c.ioInput
			world[j][i] = cell
			if cell == 255 {
				c.events <- CellFlipped{CompletedTurns: turn, Cell: util.Cell{j, i}}
			}
		}
	}

	//panic: send on closed channel. Tell the ticker when to close the channel
	finish := make(chan bool)

	//report the number of cells that are still alive every 2 seconds
	go ticker(&turn, finish, &world, c, &mu)

	//press the key to execute different commands
	go keyPress(&turn, p, &world, c, &mu)

	// TODO: Execute all turns of the Game of Life.
	//p.Threads == 1
	if p.Threads == 1 {
		for turn = 0; turn < p.Turns; turn++ {

			//create a newWorld to store the world after this turn
			newWorld := make([][]byte, p.ImageWidth)
			for i := range newWorld {
				newWorld[i] = make([]byte, p.ImageHeight)
			}

			newWorld = calculateNextState(world)
			mu.Lock()

			//initialise alive cells before processing any turns
			initializeAliveCells(world, newWorld, c, turn)

			//change the oldWorld to newWorld
			world = newWorld

			mu.Unlock()

			c.events <- TurnComplete{CompletedTurns: turn}
		}

	} else {
		//parallel
		for turn = 0; turn < p.Turns; turn++ {

			//store the aliveCells after this turn to initialise the newWorld
			var aliveCells []util.Cell

			//make a channel to divide the picture
			images := make(chan int, p.Threads)
			//make a channel to get the alive cells from workeeWorld
			cells := make(chan []util.Cell, p.ImageWidth)

			//create the workers
			for workers := 0; workers <= p.Threads; workers++ {
				go worker(images, world, p, cells)
			}

			//tell the worker how many world slice
			for w := 0; w < p.ImageWidth; w++ {
				images <- w
			}

			//create a newWorld to store the world after this turn
			newWorld := make([][]byte, p.ImageWidth)
			for i := range newWorld {
				newWorld[i] = make([]byte, p.ImageHeight)
			}

			//get all the alive cells from channel
			for i := 0; i < p.ImageWidth; i++ {
				aliveCell := <-cells
				for _, c := range aliveCell {
					aliveCells = append(aliveCells, c)
				}
			}

			//initialise the newWorld
			for _, alivePoints := range aliveCells {
				newWorld[alivePoints.X][alivePoints.Y] = 255
			}

			//close all the channels after use
			close(images)
			close(cells)

			mu.Lock()

			//initialise alive cells before processing any turns
			initializeAliveCells(world, newWorld, c, turn)

			//change the oldWorld to newWorld
			world = newWorld

			mu.Unlock()

			c.events <- TurnComplete{CompletedTurns: turn}
		}
	}

	//tell the ticker to close the channel
	finish <- true

	// TODO: Report the final state using FinalTurnCompleteEvent.

	cells := calculateAliveCells(world)

	pgmFilename := strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.Turns)
	c.ioFilename <- pgmFilename
	c.ioCommand <- ioOutput

	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			cell := world[y][x]
			c.ioOutput <- cell
		}
	}

	imageOutputEvent := ImageOutputComplete{CompletedTurns: p.Turns, Filename: pgmFilename}
	c.events <- imageOutputEvent

	completeEvent := FinalTurnComplete{p.Turns, cells}
	c.events <- completeEvent

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}

// initialise alive cells before processing any turns
func initializeAliveCells(world [][]byte, newWorld [][]byte, c distributorChannels, turn int) {
	for j := range world {
		for i := range world[0] {
			if newWorld[j][i] != world[j][i] {
				c.events <- CellFlipped{CompletedTurns: turn, Cell: util.Cell{j, i}}
			}
		}
	}
}

// press the key to execute different commands
func keyPress(turn *int, p Params, world *[][]byte, c distributorChannels, mu sync.Locker) {

	for {
		buttom := <-c.keyPresses
		switch buttom {
		//Capture the image of the current round
		case 's':
			pgmFilename := strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(*turn)
			c.ioFilename <- pgmFilename
			c.ioCommand <- ioOutput

			for y := 0; y < p.ImageWidth; y++ {
				for x := 0; x < p.ImageHeight; x++ {
					c.ioOutput <- (*world)[x][y]
				}
			}

			imageOutputEvent := ImageOutputComplete{CompletedTurns: *turn, Filename: pgmFilename}
			c.events <- imageOutputEvent

		//Capture the image of the current round and quit the process
		case 'q':
			pgmFilename := strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(*turn)
			c.ioCommand <- ioOutput
			c.ioFilename <- pgmFilename

			//mu.Lock()
			for y := 0; y < p.ImageWidth; y++ {
				for x := 0; x < p.ImageHeight; x++ {
					c.ioOutput <- (*world)[x][y]
				}
			}

			c.ioCommand <- ioCheckIdle
			<-c.ioIdle
			imageOutputEvent := ImageOutputComplete{CompletedTurns: *turn, Filename: pgmFilename}
			c.events <- imageOutputEvent

			os.Exit(0)

		//pause the process until press p again
		case 'p':

			fmt.Println(*turn)
			mu.Lock()
			for {
				//wait for the next press
				buttom = <-c.keyPresses
				if buttom == 'p' {
					break
				}
			}

			mu.Unlock()
			fmt.Println("Continuing")
		}
	}
}

// report the number of cells that are still alive every 2 seconds
func ticker(turn *int, finish chan bool, world *[][]byte, c distributorChannels, mu sync.Locker) {
	//report every 2 seconds
	ticker := time.NewTicker(2 * time.Second)

	go func() {
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				numOfCells := len(calculateAliveCells(*world))
				mu.Unlock()
				c.events <- AliveCellsCount{*turn, numOfCells}

			case <-finish:
				return

			}
		}
	}()
}

func worker(images chan int, world [][]byte, p Params, c chan []util.Cell) {
	for width := range images {
		//every slice is a workerWorld. It will return the alive cells of each workerWorld
		cells := workerWorld(width, world, p)

		//send the alive cells to the channel
		for i, aliveCell := range cells {
			cells[i] = util.Cell{width, aliveCell.X}
		}
		c <- cells
	}
}

// calculate the alive cells of the world
func workerWorld(width int, world [][]byte, p Params) []util.Cell {

	//create a newWorld to store the new workerWorld
	newWorld := make([][]byte, 1)
	newWorld[0] = make([]byte, p.ImageWidth)

	//check the newWold's neighbour
	for h, cell := range world[width] {
		aliveNeighbour := checkNeighbour(width, h, world)

		//judge which cells are alive
		if cell == 255 {
			if aliveNeighbour < 2 || aliveNeighbour > 3 {
				newWorld[0][h] = 0
			} else {
				newWorld[0][h] = 255
			}
		} else {
			if aliveNeighbour == 3 {
				newWorld[0][h] = 255
			} else {
				newWorld[0][h] = 0
			}
		}
	}

	//calculate the alive Cells and return them
	aliveCells := calculateAliveCells(newWorld)
	return aliveCells
}

// use it when thread == 1
func calculateNextState(world [][]byte) [][]byte {

	//create a newWorld to store the newWorld
	newWorld := make([][]byte, len(world))
	for i := range newWorld {
		newWorld[i] = make([]byte, len(world[i]))
	}
	//check the newWold's neighbour
	for w := range newWorld {
		for h := range newWorld[w] {
			aliveNeighbour := checkNeighbour(w, h, world)

			//judge which cells are alive
			if world[w][h] == 255 {
				if aliveNeighbour < 2 || aliveNeighbour > 3 {
					newWorld[w][h] = 0
				} else {
					newWorld[w][h] = 255
				}
			} else {
				if aliveNeighbour == 3 {
					newWorld[w][h] = 255
				} else {
					newWorld[w][h] = 0
				}
			}
		}
	}
	return newWorld
}

// check the neighbours of the cells
func checkNeighbour(width int, height int, world [][]byte) int {
	//initialise the number of neighbours
	neighbour := 0

	//get the cells' neighbour
	for i := width - 1; i <= width+1; i++ {
		for j := height - 1; j <= height+1; j++ {
			//ignore itself
			if i == width && j == height {
				continue
			}

			x := i
			y := j

			//if the cell is the bound
			if x < 0 {
				x = len(world) - 1
			}
			if x >= len(world) {
				x = 0
			}
			if y < 0 {
				y = len(world[0]) - 1
			}
			if y >= len(world[0]) {
				y = 0
			}

			if world[x][y] == 255 {
				neighbour++
			}
		}
	}
	return neighbour
}

// calculate the alive cells
func calculateAliveCells(world [][]byte) []util.Cell {

	aliveCells := []util.Cell{}

	for w, width := range world {
		for h := range width {
			if world[w][h] == 255 {
				aliveCells = append(aliveCells, util.Cell{h, w})
			}
		}
	}
	return aliveCells
}

type LocalController struct {
	c distributorChannels
}

func handleServer(c distributorChannels, done chan bool) {
	//Set the server
	//err := rpc.Register
	err := rpc.Register(&LocalController{c: c})
	if err != nil {
		log.Fatal("LocalController register: ", err)
	}
	ln, err := net.Listen("tcp", "8060")
	if err != nil {
		log.Fatal("LocalController problem: ", err)
	}
	defer func(ln net.Listener) {
		err := ln.Close()
		if err != nil {
			log.Fatal("LocalController listener closing: ", err)
			return
		}
	}(ln)
	rpc.Accept(ln)
}
