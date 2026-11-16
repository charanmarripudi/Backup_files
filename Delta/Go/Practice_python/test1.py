class Vehicle:

    def __init__(self, vehicle_id):

        self.vehicle_id = vehicle_id
        self.queue_time = 0

    def update_queue_time(self, seconds):

        self.queue_time += seconds 

car = Vehicle(101)

car.update_queue_time(5)

print(car.queue_time)